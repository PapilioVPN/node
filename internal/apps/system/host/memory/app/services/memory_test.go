package services_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/memory/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/memory/domain"
)

func TestFreshReadsPreserveValuesAndMeasureCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	values := []domain.Snapshot{{TotalBytes: 1024, AvailableBytes: 1024, FreeBytes: 1024}, {TotalBytes: 1 << 54, AvailableBytes: 1 << 53, FreeBytes: 1 << 52, UsedBytes: 1 << 53, UsedPercent: 50}}
	calls := 0
	service := services.Service{Reader: readerFunc(func(received context.Context) (domain.Snapshot, error) {
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
		got, err := service.Read(ctx)
		after := time.Now()
		if err != nil || got.Memory != want || got.ObservedAt.Location() != time.UTC || got.ObservedAt.Before(before) || got.ObservedAt.After(after) || got.Duration < 0 || got.Duration > after.Sub(before) {
			t.Fatalf("invalid measured memory: %+v %v; want %+v", got, err, want)
		}
	}
	if calls != len(values) {
		t.Fatalf("%d native reads for %d observations", calls, len(values))
	}
}

func TestFailurePreservesCauseWithoutPartialMemory(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("private kernel cause")} {
		service := services.Service{Reader: readerFunc(func(context.Context) (domain.Snapshot, error) { return domain.Snapshot{TotalBytes: 123}, cause })}
		got, err := service.Read(context.Background())
		if got != (domain.Observation{}) || !errors.Is(err, cause) {
			t.Fatalf("partial result or lost cause: %+v %v", got, err)
		}
	}
}
