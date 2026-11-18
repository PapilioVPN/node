package services_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/load/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/load/domain"
)

func TestFreshReadsPreserveWindowsAndMeasureCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	values := []domain.Averages{{}, {OneMinute: 1.25, FiveMinutes: 5.5, FifteenMinutes: 15.875}}
	calls := 0
	service := services.Service{Reader: readerFunc(func(received context.Context) (domain.Averages, error) {
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
		if err != nil || got.Averages != want || got.ObservedAt.Location() != time.UTC || got.ObservedAt.Before(before) || got.ObservedAt.After(after) || got.Duration < 0 || got.Duration > after.Sub(before) {
			t.Fatalf("invalid measured load: %+v %v; want %+v", got, err, want)
		}
	}
	if calls != len(values) {
		t.Fatalf("%d native reads for %d observations", calls, len(values))
	}
}

func TestFailurePreservesCauseWithoutPartialLoad(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("private kernel cause")} {
		service := services.Service{Reader: readerFunc(func(context.Context) (domain.Averages, error) { return domain.Averages{OneMinute: 123}, cause })}
		got, err := service.Read(context.Background())
		if got != (domain.Observation{}) || !errors.Is(err, cause) {
			t.Fatalf("partial result or lost cause: %+v %v", got, err)
		}
	}
}
