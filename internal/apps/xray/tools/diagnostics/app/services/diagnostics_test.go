package services

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/domain"
)

func TestReportHasEvidenceWhenSourcesAreDisabled(t *testing.T) {
	s, err := New([]Binding{{ID: "edge"}}, statusFunc(running), nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Report(context.Background(), domain.Request{InstanceID: "edge"})
	if err != nil || r.InstanceID != "edge" || r.Native.State != "disabled" || r.Binary.State != "disabled" {
		t.Fatalf("disabled sources became empty success: %+v %v", r, err)
	}
}

func TestFreshnessRevisionAndGenerationEvidence(t *testing.T) {
	now := time.Unix(10000, 0).UTC()
	old := now.Add(-2 * time.Minute)
	rev := uint64(math.MaxUint64)
	n := 0
	status := statusFunc(func(context.Context, string) (instances.Status, error) {
		n++
		return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: uint64(n), ConfigurationRevision: &rev, Failure: errors.New("password-private")}, ObservedAt: now}, nil
	})
	native := nativeFunc(func(context.Context) (domain.Native, error) {
		return domain.Native{Evidence: sample("native", now), Runtime: sample("runtime", now), Discovery: sample("reflection", now)}, nil
	})
	metaCalls := 0
	metadata := metadataFunc(func(context.Context, string) (configs.DesiredMetadata, error) {
		metaCalls++
		return configs.DesiredMetadata{Revision: rev, UpdatedAt: old, Durable: true, SHA256: "private-key"}, nil
	})
	s, err := New([]Binding{{ID: "edge", Native: native, APIConfigured: true, ConfigurationManaged: true}}, status, metadata)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	got, err := s.Report(context.Background(), domain.Request{InstanceID: "edge"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Consistency != "execution_changed" || got.Execution.Generation != 2 || *got.Native.Generation != 1 || got.Native.Freshness != "stale" || got.Configuration.MatchesApplied != nil || metaCalls != 1 || got.Execution.FailureReason != "execution_failed" {
		t.Fatal(got)
	}
	s.status = statusFunc(func(context.Context, string) (instances.Status, error) {
		return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: 2, ConfigurationRevision: &rev}, ObservedAt: old}, nil
	})
	got, err = s.Report(context.Background(), domain.Request{InstanceID: "edge"})
	if err != nil || got.Execution.Freshness != "stale" || got.Consistency != "execution_unverified" || got.Native.Freshness != "stale" || got.Configuration.Freshness != "fresh" {
		t.Fatal(got, err)
	}
	s.status = statusFunc(func(context.Context, string) (instances.Status, error) {
		return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: 2, ConfigurationRevision: &rev}, ObservedAt: now}, nil
	})
	got, err = s.Report(context.Background(), domain.Request{InstanceID: "edge"})
	if err != nil || got.Configuration.MatchesApplied == nil || !*got.Configuration.MatchesApplied || got.Native.Freshness != "fresh" {
		t.Fatal(got, err)
	}
}
func TestFailuresAreUsefulAndCapacityRejectsWithoutQueue(t *testing.T) {
	private := errors.New("password-private key-private /private/config")
	entered, release := make(chan struct{}), make(chan struct{})
	binary := binaryFunc(func(ctx context.Context) (domain.Binary, error) {
		close(entered)
		select {
		case <-release:
			return domain.Binary{}, private
		case <-ctx.Done():
			return domain.Binary{}, ctx.Err()
		}
	})
	s, _ := New([]Binding{{ID: "edge", Binary: binary, APIConfigured: true, ConfigurationManaged: true}}, statusFunc(running), nil)
	done := make(chan domain.Report)
	go func() { r, _ := s.Report(context.Background(), domain.Request{InstanceID: "edge"}); done <- r }()
	<-entered
	if _, err := s.Report(context.Background(), domain.Request{InstanceID: "edge"}); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	close(release)
	got := <-done
	if got.Binary.State != "unavailable" || got.Binary.Reason != "source_unavailable" || !errors.Is(got.Binary.Cause, private) || got.Native.State != "unavailable" || got.Configuration.State != "unavailable" || got.Execution.Evidence.State != "available" {
		t.Fatal(got)
	}
	for _, r := range []domain.Request{{InstanceID: ""}, {InstanceID: "edge", MaxAgeSeconds: 86401}} {
		if _, err := s.Report(context.Background(), r); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatal(err)
		}
	}
	if _, err := s.Report(context.Background(), domain.Request{InstanceID: "absent"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r, err := s.Report(ctx, domain.Request{InstanceID: "edge"}); !errors.Is(err, context.Canceled) || r.InstanceID != "" {
		t.Fatal(r, err)
	}
}

func TestGlobalAdmissionAndCancellationJoin(t *testing.T) {
	entered := make(chan struct{}, 4)
	binary := binaryFunc(func(ctx context.Context) (domain.Binary, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return domain.Binary{}, ctx.Err()
	})
	var bindings []Binding
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		bindings = append(bindings, Binding{ID: id, Binary: binary})
	}
	s, _ := New(bindings, statusFunc(running), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	for _, id := range []string{"a", "b", "c", "d"} {
		wg.Go(func() {
			r, err := s.Report(ctx, domain.Request{InstanceID: id})
			if !errors.Is(err, context.Canceled) || r.InstanceID != "" {
				t.Error(r, err)
			}
		})
		<-entered
	}
	if _, err := s.Report(ctx, domain.Request{InstanceID: "e"}); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	cancel()
	wg.Wait()
	if len(s.slots) != 0 {
		t.Fatal("admitted work not joined")
	}
}
