package services_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/memory/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/memory/domain"
)

func TestSwapFreshReadsPreserveValuesAndMeasureCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	values := []domain.SwapSnapshot{{}, {TotalBytes: 1 << 54, FreeBytes: 1 << 53, UsedBytes: 1 << 53, SwappedInBytes: 8192, SwappedOutBytes: 16384, UsedPercent: 50}}
	calls := 0
	service := services.Service{SwapReader: swapReaderFunc(func(received context.Context) (domain.SwapSnapshot, error) {
		if received != ctx {
			t.Fatal("reader did not receive caller context")
		}
		if calls >= len(values) {
			t.Fatal("unexpected extra native read")
		}
		value := values[calls]
		calls++
		return value, nil
	})}
	for _, want := range values {
		before := time.Now()
		got, err := service.ReadSwap(ctx)
		after := time.Now()
		if err != nil || got.Swap != want || got.ObservedAt.Location() != time.UTC || got.ObservedAt.Before(before) || got.ObservedAt.After(after) || got.Duration < 0 || got.Duration > after.Sub(before) {
			t.Fatalf("invalid measured swap: %+v %v; want %+v", got, err, want)
		}
	}
	if calls != len(values) {
		t.Fatalf("%d native reads for %d observations", calls, len(values))
	}
}

func TestFailurePreservesCauseWithoutPartialSwap(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("private kernel cause")} {
		service := services.Service{SwapReader: swapReaderFunc(func(context.Context) (domain.SwapSnapshot, error) { return domain.SwapSnapshot{TotalBytes: 123}, cause })}
		got, err := service.ReadSwap(context.Background())
		if got != (domain.SwapObservation{}) || !errors.Is(err, cause) {
			t.Fatalf("partial result or lost cause: %+v %v", got, err)
		}
	}
}
