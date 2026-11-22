package services_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
)

// These controlled ports model admission/outcomes, not real Xray construction.
type inspector struct{}

func (inspector) Inspect(_ context.Context, b []byte) (domain.Candidate, error) {
	tag, _, _ := strings.Cut(string(b), ":")
	digest := sha256.Sum256(b)
	return domain.Candidate{Tag: tag, Fingerprint: hex.EncodeToString(digest[:])}, nil
}

type replacementNative struct {
	mu             sync.Mutex
	records        []domain.Record
	calls          []string
	failAt         string
	failure        domain.NativeResult
	cause          error
	block, release chan struct{}
}

func (n *replacementNative) Records(context.Context) ([]domain.Record, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, "read")
	return append([]domain.Record{}, n.records...), nil
}
func (n *replacementNative) Add(ctx context.Context, b []byte) (domain.NativeResult, error) {
	return n.change("add", b)
}
func (n *replacementNative) Remove(ctx context.Context, tag string) (domain.NativeResult, error) {
	return n.change("remove", nil)
}
func (n *replacementNative) change(kind string, b []byte) (domain.NativeResult, error) {
	n.mu.Lock()
	n.calls = append(n.calls, kind)
	if n.failAt == kind {
		n.mu.Unlock()
		return n.failure, n.cause
	}
	n.mu.Unlock()
	if n.block != nil && kind == "remove" {
		close(n.block)
		<-n.release
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if kind == "remove" {
		n.records = nil
	} else {
		n.records = []domain.Record{{Tag: "managed", Configuration: append([]byte{}, b...)}}
	}
	return domain.NativeResult{Tag: "managed", Effect: domain.Completed, Acknowledged: true}, nil
}
func replacementSetup(t *testing.T, n *replacementNative) (*services.Service, *services.Replacements, *execution) {
	t.Helper()
	e := &execution{generation: 1}
	s, _ := services.New([]services.Binding{{ID: "a", Client: n}}, e)
	r, err := services.NewReplacements(s, inspector{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, r, e
}
func replaceRequest() domain.ReplacementRequest {
	return domain.ReplacementRequest{Tag: "managed", Configuration: []byte("managed:new"), DefaultPolicy: domain.MayChangeDefault, ConnectionPolicy: domain.RetainConnections}
}
func TestPlanModesAreReadOnlyAndDefaultStaysUnobservable(t *testing.T) {
	for _, test := range []struct {
		name, content string
		defaults      domain.DefaultPolicy
		connections   domain.ConnectionPolicy
		want          string
	}{
		{"live", "managed:new", domain.MayChangeDefault, domain.RetainConnections, "live_remove_add"},
		{"preserve default", "managed:new", domain.PreserveDefault, domain.RetainConnections, "configs_restart"},
		{"close connections", "managed:old", domain.MayChangeDefault, domain.TerminateConnections, "configs_restart"},
		{"unchanged", "managed:old", domain.PreserveDefault, domain.RetainConnections, "unchanged"},
	} {
		t.Run(test.name, func(t *testing.T) {
			n := &replacementNative{records: []domain.Record{{Tag: "managed", Configuration: []byte("managed:old")}}}
			_, r, _ := replacementSetup(t, n)
			request := replaceRequest()
			request.Configuration = []byte(test.content)
			request.DefaultPolicy = test.defaults
			request.ConnectionPolicy = test.connections
			p, err := r.Plan(context.Background(), "a", request)
			if err != nil || p.Mode != test.want || p.DefaultState != "unobservable" || len(n.calls) != 1 {
				t.Fatal(p, err, n.calls)
			}
			result, err := r.Apply(context.Background(), "a", p.ID)
			if test.want == "configs_restart" {
				if !errors.Is(err, domain.ErrRestartRequired) || result.Outcome != "not_applied" || len(n.calls) != 1 {
					t.Fatal(result, err, n.calls)
				}
			} else if test.want == "unchanged" {
				if err != nil || result.Outcome != "unchanged" || fmt.Sprint(n.calls) != "[read read]" {
					t.Fatal(result, err, n.calls)
				}
			} else if err != nil || result.Outcome != "completed" || fmt.Sprint(n.calls) != "[read read remove add]" {
				t.Fatal(result, err, n.calls)
			}
		})
	}
}
func TestReplacementStaleGenerationOwnerRevisionAndExternalRecord(t *testing.T) {
	for _, kind := range []string{"generation", "owner mutation", "external change"} {
		t.Run(kind, func(t *testing.T) {
			n := &replacementNative{records: []domain.Record{{Tag: "managed", Configuration: []byte("managed:old")}}}
			s, r, e := replacementSetup(t, n)
			p, _ := r.Plan(context.Background(), "a", replaceRequest())
			switch kind {
			case "generation":
				e.next()
			case "owner mutation":
				s.Add(context.Background(), "a", []byte("managed:other"))
			case "external change":
				n.records[0].Configuration = []byte("managed:changed")
			}
			before := len(n.calls)
			result, err := r.Apply(context.Background(), "a", p.ID)
			if !errors.Is(err, domain.ErrStalePlan) || result.Outcome != "not_applied" {
				t.Fatal(result, err)
			}
			for _, call := range n.calls[before:] {
				if call != "read" {
					t.Fatal("stale plan had effects", n.calls)
				}
			}
		})
	}
}
func TestReplacementPartialUnknownAndNoAutomaticReplay(t *testing.T) {
	private := errors.New("private native diagnostics")
	for _, effect := range []domain.Effect{domain.NotApplied, domain.Unknown} {
		t.Run(string(effect), func(t *testing.T) {
			n := &replacementNative{records: []domain.Record{{Tag: "managed", Configuration: []byte("managed:old")}}, failAt: "add", failure: domain.NativeResult{Effect: effect, RequiresInstanceRestart: effect == domain.Unknown}, cause: private}
			s, r, _ := replacementSetup(t, n)
			p, _ := r.Plan(context.Background(), "a", replaceRequest())
			result, err := r.Apply(context.Background(), "a", p.ID)
			want := "partial"
			if effect == domain.Unknown {
				want = "unknown"
			}
			if !errors.Is(err, private) || result.Outcome != want || !result.Steps[0].Acknowledged || result.Steps[1].Effect != string(effect) || !result.NativeResourcesMayRemain || strings.Contains(result.Error, "private") {
				t.Fatal(result, err)
			}
			before := len(n.calls)
			again, _ := r.Apply(context.Background(), "a", p.ID)
			if again.Outcome != want || len(n.calls) != before {
				t.Fatal("replayed", again, n.calls)
			}
			got, _ := r.Result(context.Background(), "a", p.ID)
			got.Steps[0].Effect = "corrupted"
			got, _ = r.Result(context.Background(), "a", p.ID)
			if got.Steps[0].Effect != "completed" {
				t.Fatal("mutable retained evidence", got)
			}
			if effect == domain.Unknown {
				if _, err := s.Records(context.Background(), "a"); !errors.Is(err, domain.ErrUncertain) {
					t.Fatal(err)
				}
			}
		})
	}
}
func TestReplacementResultReadableDuringNativeWait(t *testing.T) {
	n := &replacementNative{records: []domain.Record{{Tag: "managed", Configuration: []byte("managed:old")}}, block: make(chan struct{}), release: make(chan struct{})}
	_, r, _ := replacementSetup(t, n)
	p, _ := r.Plan(context.Background(), "a", replaceRequest())
	done := make(chan error, 1)
	go func() { _, err := r.Apply(context.Background(), "a", p.ID); done <- err }()
	<-n.block
	status, err := r.Result(context.Background(), "a", p.ID)
	if err != nil || status.Outcome != "in_progress" || status.Steps[0].Effect != "in_progress" {
		t.Fatal(status, err)
	}
	again, err := r.Apply(context.Background(), "a", p.ID)
	if err != nil || again.Outcome != "in_progress" {
		t.Fatal(again, err)
	}
	close(n.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
