package services

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/statistics/domain"
)

type readerFunc func(context.Context) (domain.Statistics, error)

func (f readerFunc) Read(ctx context.Context) (domain.Statistics, error) { return f(ctx) }

type statusFunc func(context.Context, string) (instances.Status, error)

func (f statusFunc) Status(ctx context.Context, id string) (instances.Status, error) {
	return f(ctx, id)
}
func runningStatus(context.Context, string) (instances.Status, error) {
	return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: 7}}, nil
}
func TestOneNativeReadAndExecutionConsistency(t *testing.T) {
	reads, statuses := 0, 0
	generation := uint64(7)
	service, err := New([]Binding{{ID: "a", Reader: readerFunc(func(context.Context) (domain.Statistics, error) {
		reads++
		return domain.Statistics{LiveObjects: 9}, nil
	})}, {ID: "disabled"}}, statusFunc(func(ctx context.Context, id string) (instances.Status, error) {
		statuses++
		result, _ := runningStatus(ctx, id)
		result.Execution.Generation = generation
		if statuses == 4 {
			result.Execution.Generation++
		}
		return result, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Read(context.Background(), "a")
	if err != nil || result.Statistics.LiveObjects != 9 || result.Generation != 7 || result.ObservedAt.IsZero() || result.Duration < 0 || reads != 1 || statuses != 2 {
		t.Fatalf("%+v %v reads=%d statuses=%d", result, err, reads, statuses)
	}
	result, err = service.Read(context.Background(), "a")
	if !errors.Is(err, domain.ErrExecutionChanged) || result.InstanceID != "" {
		t.Fatal("mixed execution counters returned as a successful sample")
	}
	for _, test := range []struct {
		id   string
		want error
	}{{"../bad", domain.ErrInvalidArgument}, {"missing", domain.ErrNotFound}, {"disabled", domain.ErrUnsupported}} {
		if _, err := service.Read(context.Background(), test.id); !errors.Is(err, test.want) {
			t.Fatal(test.id, err)
		}
	}
	if reads != 2 {
		t.Fatal("invalid selection reached native API")
	}
}
func TestStoppedNativeFailureAndLateCancellationDoNotBecomeZeroStats(t *testing.T) {
	cause := errors.New("private native failure")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := false
	service, _ := New([]Binding{{ID: "a", Reader: readerFunc(func(context.Context) (domain.Statistics, error) { called = true; return domain.Statistics{}, cause })}}, statusFunc(func(context.Context, string) (instances.Status, error) { return instances.Status{}, nil }))
	if _, err := service.Read(ctx, "a"); !errors.Is(err, domain.ErrNotRunning) || called {
		t.Fatal("stopped instance reached endpoint", err)
	}
	service.status = statusFunc(runningStatus)
	if result, err := service.Read(ctx, "a"); !errors.Is(err, cause) || result.InstanceID != "" {
		t.Fatal("failure became successful zero counters")
	}
	service.entries["a"] = entry{slots: make(chan struct{}, 8), reader: readerFunc(func(context.Context) (domain.Statistics, error) {
		cancel()
		return domain.Statistics{LiveObjects: 9}, nil
	})}
	if _, err := service.Read(ctx, "a"); !errors.Is(err, context.Canceled) {
		t.Fatal("late cancellation hidden", err)
	}
}
func TestBoundedConcurrentReadsRejectOverloadWithoutQueueing(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{}, 8)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	service, _ := New([]Binding{{ID: "a", Reader: readerFunc(func(ctx context.Context) (domain.Statistics, error) {
		calls.Add(1)
		entered <- struct{}{}
		<-ctx.Done()
		return domain.Statistics{}, ctx.Err()
	})}}, statusFunc(runningStatus))
	var joined sync.WaitGroup
	for range 8 {
		joined.Go(func() { service.Read(ctx, "a") })
	}
	for range 8 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if _, err := service.Read(ctx, "a"); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal("overload was queued", err)
	}
	cancel()
	joined.Wait()
	if calls.Load() != 8 || len(service.slots) != 0 || len(service.entries["a"].slots) != 0 {
		t.Fatal("admitted reads were not joined/released")
	}
}

func TestGlobalCapacityAcrossIndependentInstances(t *testing.T) {
	entered := make(chan struct{}, 32)
	read := readerFunc(func(ctx context.Context) (domain.Statistics, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return domain.Statistics{}, ctx.Err()
	})
	service, _ := New([]Binding{{ID: "a", Reader: read}, {ID: "b", Reader: read}, {ID: "c", Reader: read}, {ID: "d", Reader: read}, {ID: "e", Reader: read}}, statusFunc(runningStatus))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var joined sync.WaitGroup
	for _, id := range []string{"a", "b", "c", "d"} {
		for range 8 {
			joined.Go(func() { service.Read(ctx, id) })
		}
	}
	for range 32 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if _, err := service.Read(ctx, "e"); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal("global native work was not bounded", err)
	}
	cancel()
	joined.Wait()
	if len(service.slots) != 0 {
		t.Fatal("global slots leaked")
	}
}
