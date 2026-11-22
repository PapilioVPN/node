package services

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

func TestExactObservationAndOneTargetedRead(t *testing.T) {
	calls := 0
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "context")
	reader := readerFunc(func(got context.Context, selection domain.Selection) (int64, error) {
		calls++
		if got != ctx || selection != exact {
			t.Fatal("selection/context changed")
		}
		return 0, nil
	})
	s, err := New([]Binding{{ID: "a", Reader: reader}, {ID: "disabled"}}, statusFunc(running))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	got, err := s.Read(ctx, "a", exact)
	if err != nil || calls != 1 || got.Bytes != 0 || got.Selection != exact || got.Generation != 7 || got.Source != domain.Source || got.InstanceID != "a" || got.ObservedAt.Before(start) || got.ObservedAt.Location() != time.UTC || got.Duration < 0 {
		t.Fatalf("observation=%+v err=%v reads=%d", got, err, calls)
	}
	for _, test := range []struct {
		id        string
		selection domain.Selection
		want      error
	}{
		{"missing", exact, domain.ErrNotFound}, {"disabled", exact, domain.ErrUnsupported}, {"a", domain.Selection{}, domain.ErrInvalidArgument}, {"", exact, domain.ErrInvalidArgument},
	} {
		got, err := s.Read(ctx, test.id, test.selection)
		if !errors.Is(err, test.want) || got != (domain.Observation{}) {
			t.Fatalf("failed selection %+v %v", got, err)
		}
	}
	if calls != 1 {
		t.Fatal("preflight rejection dispatched", calls)
	}
}

func TestReadFailuresAndExecutionChangesDiscardFacts(t *testing.T) {
	private := errors.New("private native failure")
	for _, test := range []struct {
		name           string
		state          instances.State
		change, cancel bool
		cause, want    error
	}{
		{"missing counter", instances.Running, false, false, domain.ErrCounterNotFound, domain.ErrCounterNotFound},
		{"failure", instances.Running, false, false, private, private},
		{"changed", instances.Running, true, false, private, domain.ErrExecutionChanged},
		{"stopped", instances.Stopped, false, false, nil, domain.ErrNotRunning},
		{"canceled after read", instances.Running, false, true, nil, context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reads, statuses := 0, 0
			s, _ := New([]Binding{{ID: "a", Reader: readerFunc(func(context.Context, domain.Selection) (int64, error) {
				reads++
				if test.cancel {
					cancel()
				}
				return 123, test.cause
			})}}, statusFunc(func(context.Context, string) (instances.Status, error) {
				statuses++
				generation := uint64(1)
				if test.change && statuses > 1 {
					generation++
				}
				return instances.Status{Execution: instances.Execution{State: test.state, Generation: generation}}, nil
			}))
			got, err := s.Read(ctx, "a", exact)
			if !errors.Is(err, test.want) || (test.cause != nil && !errors.Is(err, test.cause)) || got != (domain.Observation{}) {
				t.Fatalf("facts=%+v error=%v", got, err)
			}
			wantReads := 1
			if test.state != instances.Running {
				wantReads = 0
			}
			if reads != wantReads {
				t.Fatal("unexpected native reads", reads)
			}
		})
	}
}

func TestAdmissionBoundsCancellationAndSlotRelease(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprint("global=", global), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered := make(chan struct{}, 32)
			reader := readerFunc(func(ctx context.Context, _ domain.Selection) (int64, error) {
				entered <- struct{}{}
				<-ctx.Done()
				return 0, ctx.Err()
			})
			bindings := []Binding{{ID: "a", Reader: reader}}
			count := 8
			if global {
				count = 32
				for i := 0; i < 4; i++ {
					bindings = append(bindings, Binding{ID: fmt.Sprint("b", i), Reader: reader})
				}
			}
			s, _ := New(bindings, statusFunc(running))
			var joined sync.WaitGroup
			for i := 0; i < count; i++ {
				id := "a"
				if global {
					id = fmt.Sprint("b", i/8)
				}
				joined.Go(func() {
					if _, err := s.Read(ctx, id, exact); !errors.Is(err, context.Canceled) {
						t.Errorf("read error %v", err)
					}
				})
			}
			for range count {
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("admission did not enter")
				}
			}
			if _, err := s.Read(context.Background(), "a", exact); !errors.Is(err, domain.ErrCapacity) {
				t.Fatal("overload queued or accepted", err)
			}
			cancel()
			joined.Wait()
			if len(s.slots) != 0 {
				t.Fatal("global slots leaked")
			}
			for _, entry := range s.entries {
				if len(entry.slots) != 0 {
					t.Fatal("instance slots leaked")
				}
			}
		})
	}
}

func TestBindingValidationAndCanceledPreflight(t *testing.T) {
	for _, bindings := range [][]Binding{{{ID: ""}}, {{ID: "a"}, {ID: "a"}}} {
		if _, err := New(bindings, statusFunc(running)); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatal(err)
		}
	}
	if _, err := New(nil, nil); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatal(err)
	}
	s, _ := New(nil, statusFunc(running))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Read(ctx, "a", exact); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestSameCounterIdentityRemainsInstanceScoped(t *testing.T) {
	callsA, callsB := 0, 0
	s, err := New([]Binding{
		{ID: "a", Reader: readerFunc(func(context.Context, domain.Selection) (int64, error) { callsA++; return 123, nil })},
		{ID: "b", Reader: readerFunc(func(context.Context, domain.Selection) (int64, error) { callsB++; return 456, nil })},
	}, statusFunc(running))
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Read(context.Background(), "b", exact)
	if err != nil || result.InstanceID != "b" || result.Bytes != 456 || callsA != 0 || callsB != 1 {
		t.Fatalf("wrong instance observation %+v %v calls=%d/%d", result, err, callsA, callsB)
	}
}
