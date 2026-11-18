package services_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/uptime/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/uptime/domain"
)

func TestReadMeasuresEachObservationWithoutCaching(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	values := []time.Duration{0, 43*time.Hour + 123456789*time.Nanosecond}
	calls := 0
	service := services.Service{Reader: readerFunc(func(received context.Context) (time.Duration, error) {
		if received != ctx {
			t.Fatal("reader did not receive the caller's context")
		}
		if calls >= len(values) {
			t.Fatal("unexpected extra reader call")
		}
		value := values[calls]
		calls++
		return value, nil
	})}
	for _, want := range values {
		before := time.Now()
		result, err := service.Read(ctx)
		after := time.Now()
		if err != nil || result.Uptime != want {
			t.Fatalf("uptime read: %+v, %v; want %v", result, err, want)
		}
		if result.ObservedAt.Before(before) || result.ObservedAt.After(after) || result.ObservedAt.Location() != time.UTC || result.Duration < 0 || result.Duration > after.Sub(before) {
			t.Fatalf("invalid observation timing: %+v", result)
		}
	}
	if calls != len(values) {
		t.Fatalf("got %d reader calls for %d observations", calls, len(values))
	}
}

func TestFailedReadPreservesCauseWithoutPartialObservation(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("kernel clock unavailable")} {
		t.Run(cause.Error(), func(t *testing.T) {
			service := services.Service{Reader: readerFunc(func(context.Context) (time.Duration, error) {
				return time.Hour, cause
			})}
			result, err := service.Read(context.Background())
			if !errors.Is(err, cause) || result != (domain.Observation{}) {
				t.Fatalf("failed read lost cause or returned facts: %+v, %v", result, err)
			}
		})
	}
}
