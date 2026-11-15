package app_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/app"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	instances "github.com/PapilioVPN/node/internal/modules/instances/domain"
)

type trafficReader func(context.Context, domain.TrafficSelection) (domain.TrafficCounter, error)

func (f trafficReader) ReadTrafficCounter(ctx context.Context, selection domain.TrafficSelection) (domain.TrafficCounter, error) {
	return f(ctx, selection)
}

func TestTrafficCounterSelectsExactlyOneBindingAndPreservesSignedBytes(t *testing.T) {
	selection := domain.TrafficSelection{Scope: domain.UserTraffic, Name: " Mixed@Example ", Direction: domain.Downlink}
	ctx := t.Context()
	counter := domain.TrafficCounter{Bytes: math.MinInt64, ObservedAt: time.Now()}
	calls := 0
	bindings := map[instances.InstanceID]app.TrafficCounterReader{
		"edge": trafficReader(func(received context.Context, query domain.TrafficSelection) (domain.TrafficCounter, error) {
			calls++
			if received != ctx || query != selection {
				t.Fatal("selection/context changed")
			}
			return counter, nil
		}),
		"other": trafficReader(func(context.Context, domain.TrafficSelection) (domain.TrafficCounter, error) {
			t.Fatal("unselected native API called")
			return domain.TrafficCounter{}, nil
		}),
		"disabled": nil,
	}
	service := app.NewTrafficCounterService(bindings)
	delete(bindings, "edge")
	result, err := service.GetTrafficCounter(ctx, "edge", selection)
	if err != nil || result.InstanceID != "edge" || result.Selection != selection || result.Counter != counter || calls != 1 {
		t.Fatalf("selected observation: %+v, %v; calls=%d", result, err, calls)
	}
	for id, expected := range map[instances.InstanceID]error{" edge ": instances.ErrNotFound, "missing": instances.ErrNotFound, "disabled": domain.ErrAPIDisabled} {
		if result, err := service.GetTrafficCounter(ctx, id, selection); !errors.Is(err, expected) || result != (domain.InstanceTrafficCounter{}) {
			t.Fatalf("selection %q: %+v, %v", id, result, err)
		}
	}
	if calls != 1 {
		t.Fatal("absent/disabled selection reached the API")
	}
}

func TestTrafficCounterDiscardsFailedOrCanceledFacts(t *testing.T) {
	for _, cancelDuringRead := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		calls := 0
		service := app.NewTrafficCounterService(map[instances.InstanceID]app.TrafficCounterReader{
			"edge": trafficReader(func(context.Context, domain.TrafficSelection) (domain.TrafficCounter, error) {
				calls++
				if cancelDuringRead {
					cancel()
				}
				return domain.TrafficCounter{Bytes: 99}, domain.ErrTrafficCounterNotFound
			}),
		})
		result, err := service.GetTrafficCounter(ctx, "edge", domain.TrafficSelection{})
		cancel()
		if result != (domain.InstanceTrafficCounter{}) || !errors.Is(err, domain.ErrTrafficCounterNotFound) || cancelDuringRead && !errors.Is(err, context.Canceled) {
			t.Fatalf("partial facts or lost cause: %+v, %v", result, err)
		}
		if _, err := service.GetTrafficCounter(ctx, "edge", domain.TrafficSelection{}); !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("canceled admission reached API: %v; calls=%d", err, calls)
		}
	}
}
