package services_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
)

func TestUsageUsesExactlyTwoNativeReadsForGrowingSelections(t *testing.T) {
	for _, count := range []int{1, 64, 4096} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		calls := 0
		service := services.Service{TimesReader: timesReaderFunc(func(received context.Context, perCPU bool) (domain.Times, error) {
			if received != ctx || perCPU != (count > 1) {
				t.Fatal("lost context/selection")
			}
			calls++
			rows := make([]domain.Ticks, count)
			for i := range rows {
				rows[i] = domain.Ticks{CPU: fmt.Sprintf("cpu%d", i), User: 1<<63 + uint64(calls)*25, Idle: 1<<63 + uint64(calls)*75}
			}
			return domain.Times{PerCPU: perCPU, TicksPerSecond: 100, Counters: rows}, nil
		})}
		before := time.Now()
		result, err := service.SampleUsage(ctx, domain.UsageRequest{PerCPU: count > 1, Interval: time.Millisecond})
		cancel()
		if err != nil || calls != 2 || len(result.CPUs) != count || result.SampleInterval < time.Millisecond || result.RequestedInterval != time.Millisecond || result.StartedAt.Before(before) || result.ObservedAt.Before(result.StartedAt) || result.ObservedAt.After(time.Now()) || result.Duration < result.SampleInterval || result.Duration > time.Since(before) {
			t.Fatalf("invalid sample for %d CPUs: %+v %v; calls=%d", count, result, err, calls)
		}
		for _, row := range result.CPUs {
			if row.Percent == nil || *row.Percent != 25 || row.State != domain.UsageMeasured || row.TotalTicks == nil || *row.TotalTicks != 100 || row.BusyTicks == nil || *row.BusyTicks != 25 {
				t.Fatalf("incorrect sampled usage: %+v", row)
			}
		}
	}
}

func TestUsageRejectsInvalidIntervalAndPreCanceledContextBeforeIO(t *testing.T) {
	service := services.Service{}
	for _, interval := range []time.Duration{0, -1} {
		result, err := service.SampleUsage(context.Background(), domain.UsageRequest{Interval: interval})
		if !errors.Is(err, domain.ErrInvalidSampleInterval) || !reflect.DeepEqual(result, domain.UsageObservation{}) {
			t.Fatalf("invalid interval accepted: %+v %v", result, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result, err := service.SampleUsage(ctx, domain.UsageRequest{Interval: time.Millisecond}); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, domain.UsageObservation{}) {
		t.Fatalf("pre-canceled sample: %+v %v", result, err)
	}
}

func TestUsageCancelsWaitWithoutSecondReadOrPartialResult(t *testing.T) {
	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	service := services.Service{TimesReader: timesReaderFunc(func(context.Context, bool) (domain.Times, error) {
		calls++
		close(ready)
		return domain.Times{TicksPerSecond: 100}, nil
	})}
	type outcome struct {
		result domain.UsageObservation
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := service.SampleUsage(ctx, domain.UsageRequest{Interval: time.Hour})
		done <- outcome{result, err}
	}()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("first sample did not complete")
	}
	cancel()
	select {
	case got := <-done:
		if calls != 1 || !errors.Is(got.err, context.Canceled) || !reflect.DeepEqual(got.result, domain.UsageObservation{}) {
			t.Fatalf("incomplete cancellation: %+v %v; calls=%d", got.result, got.err, calls)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sample wait did not stop")
	}
}

func TestUsagePreservesFailuresAtEitherReadWithoutPartialSuccess(t *testing.T) {
	cause := errors.New("controlled stat read failure")
	for _, failAt := range []int{1, 2} {
		calls := 0
		service := services.Service{TimesReader: timesReaderFunc(func(context.Context, bool) (domain.Times, error) {
			calls++
			if calls == failAt {
				return domain.Times{Counters: []domain.Ticks{{CPU: "cpu0", User: 42}}}, cause
			}
			return domain.Times{TicksPerSecond: 100}, nil
		})}
		result, err := service.SampleUsage(context.Background(), domain.UsageRequest{Interval: time.Nanosecond})
		if calls != failAt || !errors.Is(err, cause) || !reflect.DeepEqual(result, domain.UsageObservation{}) {
			t.Fatalf("lost read failure: %+v %v; calls=%d", result, err, calls)
		}
	}
}

func TestConcurrentUsageRequestsNeverShareBaselines(t *testing.T) {
	type requestKey struct{}
	var mu sync.Mutex
	calls := map[int]int{}
	service := services.Service{TimesReader: timesReaderFunc(func(ctx context.Context, _ bool) (domain.Times, error) {
		id := ctx.Value(requestKey{}).(int)
		mu.Lock()
		calls[id]++
		step := calls[id]
		mu.Unlock()
		busy := uint64(id) * 25
		return domain.Times{TicksPerSecond: 100, Counters: []domain.Ticks{{CPU: "cpu-total", User: uint64(step) * busy, Idle: uint64(step) * (100 - busy)}}}, nil
	})}
	var group sync.WaitGroup
	for id := 1; id <= 3; id++ {
		group.Add(1)
		go func(id int) {
			defer group.Done()
			ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), requestKey{}, id), 2*time.Second)
			defer cancel()
			got, err := service.SampleUsage(ctx, domain.UsageRequest{Interval: time.Millisecond})
			if err != nil || len(got.CPUs) != 1 || got.CPUs[0].Percent == nil || *got.CPUs[0].Percent != float64(id*25) {
				t.Errorf("cross-request baseline for %d: %+v %v", id, got, err)
			}
		}(id)
	}
	group.Wait()
	for id, count := range calls {
		if count != 2 {
			t.Fatalf("request %d performed %d reads", id, count)
		}
	}
}
