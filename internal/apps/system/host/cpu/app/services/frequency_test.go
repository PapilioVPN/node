package services_test

import (
	"context"
	"errors"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
	"reflect"
	"testing"
	"time"
)

func TestFrequencyServiceMakesOneFreshReadAndPreservesAbsence(t *testing.T) {
	for _, size := range []int{0, 1, 64, 4096} {
		calls := 0
		expected := domain.Frequencies{InterfacePresent: true, Policies: make([]domain.FrequencyPolicy, size)}
		for i := range expected.Policies {
			expected.Policies[i].ID = uint32(i)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		service := services.Service{FrequencyReader: frequencyReaderFunc(func(got context.Context) (domain.Frequencies, error) {
			if got != ctx {
				t.Error("context replaced")
			}
			calls++
			return expected, nil
		})}
		started := time.Now()
		got, err := service.ReadFrequency(ctx)
		cancel()
		if err != nil || calls != 1 || !reflect.DeepEqual(got.Frequencies, expected) || got.ObservedAt.Before(started) || got.ObservedAt.After(time.Now()) || got.Duration < 0 || got.Duration > time.Since(started) {
			t.Fatalf("frequency observation: %+v %v; calls=%d", got, err, calls)
		}
	}
}

func TestFrequencyServiceFailsWithoutPartialFacts(t *testing.T) {
	cause := errors.New("native failure")
	for _, failure := range []error{cause, context.Canceled, context.DeadlineExceeded} {
		service := services.Service{FrequencyReader: frequencyReaderFunc(func(context.Context) (domain.Frequencies, error) {
			return domain.Frequencies{InterfacePresent: true}, failure
		})}
		got, err := service.ReadFrequency(context.Background())
		if !errors.Is(err, failure) || got.Frequencies.InterfacePresent || !got.ObservedAt.IsZero() {
			t.Fatalf("partial failure: %+v %v", got, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	service := services.Service{FrequencyReader: frequencyReaderFunc(func(context.Context) (domain.Frequencies, error) {
		cancel()
		return domain.Frequencies{InterfacePresent: true}, nil
	})}
	if _, err := service.ReadFrequency(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
