package services_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/identity/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/identity/domain"
)

type readerFunc func(context.Context) (domain.Identity, error)

func (read readerFunc) Read(ctx context.Context) (domain.Identity, error) { return read(ctx) }

func TestReadMeasuresOneFreshObservation(t *testing.T) {
	want := domain.Identity{Hostname: "fixture-host", OperatingSystem: "Linux", Release: "fixture-release", Version: "fixture-version", Machine: "fixture-machine"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	calls := 0
	service := services.Service{Reader: readerFunc(func(received context.Context) (domain.Identity, error) {
		calls++
		if received != ctx {
			t.Fatal("reader did not receive the caller's context")
		}
		return want, nil
	})}
	before := time.Now()
	result, err := service.Read(ctx)
	after := time.Now()
	if err != nil || result.Identity != want || calls != 1 {
		t.Fatalf("read: result=%+v calls=%d error=%v", result, calls, err)
	}
	if result.ObservedAt.Before(before) || result.ObservedAt.After(after) || result.ObservedAt.Location() != time.UTC || result.Duration < 0 || result.Duration > after.Sub(before) {
		t.Fatalf("invalid observation timing: %+v", result)
	}
}

func TestFailedReadPreservesCauseWithoutSuccessfulFacts(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("native read failed")} {
		t.Run(cause.Error(), func(t *testing.T) {
			service := services.Service{Reader: readerFunc(func(context.Context) (domain.Identity, error) {
				return domain.Identity{Hostname: "incomplete"}, cause
			})}
			result, err := service.Read(context.Background())
			if !errors.Is(err, cause) || result != (domain.Observation{}) {
				t.Fatalf("failed read became successful or lost its cause: %+v, %v", result, err)
			}
		})
	}
}
