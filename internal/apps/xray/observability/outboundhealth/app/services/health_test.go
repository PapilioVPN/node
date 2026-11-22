package services

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

func TestExactEvidenceAndFreshness(t *testing.T) {
	at := time.Unix(1000, 0)
	records := []domain.Record{
		{Tag: "fresh", Alive: true, DelayMS: 0, LastTryUnixSec: 990, LastSeenUnixSec: 990},
		{Tag: "failed", DelayMS: 99999999, LastTryUnixSec: 990, LastSeenUnixSec: 100},
		{Tag: "stale", Alive: true, DelayMS: 12, LastTryUnixSec: 900},
		{Tag: "burst", Alive: true, DelayMS: 3, HealthPing: &domain.HealthPing{All: 3, Fail: 1, AverageNS: 3000123}},
		{Tag: "future", Alive: true, LastTryUnixSec: 1001},
		{Tag: "extreme", LastTryUnixSec: math.MaxInt64, LastSeenUnixSec: math.MinInt64, DelayMS: math.MinInt64},
		{Tag: ""}, {Tag: " opaque />>> "},
	}
	s, calls := newService(t, records)
	s.now = func() time.Time { return at }
	for _, tt := range []struct {
		tag, freshness, mode string
		latency              bool
		sample               bool
	}{
		{"fresh", "fresh", "ordinary", true, true}, {"failed", "fresh", "ordinary", false, true}, {"stale", "stale", "ordinary", true, true},
		{"burst", "unknown", "burst", true, false}, {"future", "unknown", "ordinary", true, true}, {"extreme", "unknown", "ordinary", false, false},
		{"", "unknown", "ordinary", false, false}, {" opaque />>> ", "unknown", "ordinary", false, false},
	} {
		t.Run(tt.tag, func(t *testing.T) {
			got, err := s.Get(context.Background(), "edge", domain.Selection{Tag: tt.tag})
			if err != nil || got.Status == nil {
				t.Fatal(got, err)
			}
			v := got.Status
			if v.Freshness != tt.freshness || v.Mode != tt.mode || (v.LatencyMS != nil) != tt.latency || (v.SampledAt != nil) != tt.sample || got.Generation != 7 || got.Source != domain.Source || !got.ObservedAt.Equal(at) {
				t.Fatalf("wrong evidence %+v", got)
			}
		})
	}
	missing, err := s.Get(context.Background(), "edge", domain.Selection{Tag: "missing"})
	if err != nil || missing.Status != nil || missing.ObservedAt.IsZero() {
		t.Fatal(missing, err)
	}
	if *calls != len(records)+1 {
		t.Fatal(*calls)
	}
	boundary, err := s.Get(context.Background(), "edge", domain.Selection{Tag: "fresh", MaxSampleAgeSeconds: 10})
	if err != nil || boundary.Status.Freshness != "fresh" {
		t.Fatal(boundary, err)
	}
	stale, _ := s.Get(context.Background(), "edge", domain.Selection{Tag: "fresh", MaxSampleAgeSeconds: 9})
	if stale.Status.Freshness != "stale" {
		t.Fatal(stale)
	}
}
func TestExactDuplicateAndNativeFailuresReturnNoFacts(t *testing.T) {
	s, _ := newService(t, []domain.Record{{Tag: "dup"}, {Tag: "dup"}})
	if got, err := s.Get(context.Background(), "edge", domain.Selection{Tag: "dup"}); !errors.Is(err, domain.ErrUnavailable) || got.Status != nil {
		t.Fatal(got, err)
	}
	secret := errors.New("private native cause")
	s.entries["edge"] = entry{reader: readerFunc(func(context.Context) ([]domain.Record, error) { return []domain.Record{{Tag: "bad"}}, secret }), slots: make(chan struct{}, 8)}
	got, err := s.Get(context.Background(), "edge", domain.Selection{Tag: "bad"})
	if !errors.Is(err, secret) || !got.ObservedAt.IsZero() {
		t.Fatal(got, err)
	}
}
func TestSelectionAndExecutionBoundaries(t *testing.T) {
	s, calls := newService(t, nil)
	s.entries["disabled"] = entry{}
	for _, tt := range []struct {
		id        string
		selection domain.Selection
		want      error
	}{
		{"", domain.Selection{}, domain.ErrInvalidArgument}, {"absent", domain.Selection{}, domain.ErrNotFound},
		{"disabled", domain.Selection{}, domain.ErrDisabled}, {"edge", domain.Selection{Tag: strings.Repeat("x", 4097)}, domain.ErrInvalidArgument},
		{"edge", domain.Selection{Tag: string([]byte{255})}, domain.ErrInvalidArgument}, {"edge", domain.Selection{MaxSampleAgeSeconds: 86401}, domain.ErrInvalidArgument},
	} {
		if _, err := s.Get(context.Background(), tt.id, tt.selection); !errors.Is(err, tt.want) {
			t.Fatal(err, tt.want)
		}
	}
	if *calls != 0 {
		t.Fatal(*calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Get(ctx, "edge", domain.Selection{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	s.status = statusFunc(func(context.Context, string) (instances.Status, error) {
		return instances.Status{Execution: instances.Execution{State: instances.Stopped}}, nil
	})
	if _, err := s.Get(context.Background(), "edge", domain.Selection{}); !errors.Is(err, domain.ErrNotRunning) {
		t.Fatal(err)
	}
	n := 0
	s.status = statusFunc(func(context.Context, string) (instances.Status, error) {
		n++
		return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: uint64(n)}}, nil
	})
	if got, err := s.Get(context.Background(), "edge", domain.Selection{}); !errors.Is(err, domain.ErrExecutionChanged) || !got.ObservedAt.IsZero() {
		t.Fatal(got, err)
	}
}
func TestReadAdmissionAndJoinedCancellation(t *testing.T) {
	entered := make(chan struct{}, 32)
	release := make(chan struct{})
	var active, maxActive atomic.Int32
	reader := readerFunc(func(ctx context.Context) ([]domain.Record, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maxActive.Load(); n > old; old = maxActive.Load() {
			if maxActive.CompareAndSwap(old, n) {
				break
			}
		}
		entered <- struct{}{}
		select {
		case <-release:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	var bindings []Binding
	for i := 0; i < 5; i++ {
		bindings = append(bindings, Binding{ID: fmt.Sprintf("edge-%d", i), Reader: reader})
	}
	s, err := New(bindings, statusFunc(running))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Go(func() { _, _ = s.Get(ctx, fmt.Sprintf("edge-%d", i/8), domain.Selection{}) })
		<-entered
	}
	if _, err := s.Get(ctx, "edge-4", domain.Selection{}); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	cancel()
	wg.Wait()
	if active.Load() != 0 || maxActive.Load() != 32 {
		t.Fatal(active.Load(), maxActive.Load())
	}
	ctx = context.Background()
	for i := 0; i < 8; i++ {
		wg.Go(func() { _, _ = s.Get(ctx, "edge-0", domain.Selection{}) })
		<-entered
	}
	if _, err := s.Get(ctx, "edge-0", domain.Selection{}); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
}
