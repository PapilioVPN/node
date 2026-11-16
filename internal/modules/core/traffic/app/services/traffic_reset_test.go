package services_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/core/traffic/app/services"
	"github.com/PapilioVPN/node/internal/modules/core/traffic/domain"
)

type trafficResetter func(context.Context, domain.TrafficSelection) (domain.TrafficCounterReset, error)

func (f trafficResetter) ResetTrafficCounter(ctx context.Context, selection domain.TrafficSelection) (domain.TrafficCounterReset, error) {
	return f(ctx, selection)
}

func TestResetSelectsOneInstanceAndRetainsLateAcknowledgement(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	selection := domain.TrafficSelection{Scope: domain.UserTraffic, Name: " Mixed>>>user ", Direction: domain.Downlink}
	acknowledgement := domain.TrafficCounterReset{PreviousBytes: math.MinInt64, AcknowledgedAt: time.Now()}
	calls := 0
	bindings := map[instances.InstanceID]services.TrafficCounterResetter{
		" edge ": trafficResetter(func(received context.Context, query domain.TrafficSelection) (domain.TrafficCounterReset, error) {
			calls++
			if received != ctx || query != selection {
				t.Fatal("reset changed context or selection")
			}
			cancel()
			return acknowledgement, nil
		}),
		"other": trafficResetter(func(context.Context, domain.TrafficSelection) (domain.TrafficCounterReset, error) {
			t.Fatal("reset crossed instance ownership")
			return domain.TrafficCounterReset{}, nil
		}),
		"disabled": nil,
	}
	service := services.NewTrafficCounterResetService(bindings)
	delete(bindings, " edge ")
	result, err := service.ResetTrafficCounter(ctx, " edge ", selection)
	if err != nil || result.InstanceID != " edge " || result.Selection != selection || result.Reset != acknowledgement || calls != 1 {
		t.Fatalf("acknowledged reset lost: %+v %v", result, err)
	}
	if _, err := service.ResetTrafficCounter(ctx, " edge ", selection); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("pre-canceled request reached resetter")
	}
	for id, cause := range map[instances.InstanceID]error{"edge": instances.ErrNotFound, "missing": instances.ErrNotFound, "disabled": domain.ErrAPIDisabled} {
		if result, err := service.ResetTrafficCounter(t.Context(), id, selection); result != (domain.InstanceTrafficCounterReset{}) || !errors.Is(err, cause) {
			t.Fatalf("selection %q: %+v %v", id, result, err)
		}
	}
}

func TestResetPreservesUnknownEffectAndNeverRetries(t *testing.T) {
	cause := errors.Join(domain.ErrTrafficResetUnknown, context.DeadlineExceeded, errors.New("private native cause"))
	calls := 0
	service := services.NewTrafficCounterResetService(map[instances.InstanceID]services.TrafficCounterResetter{
		"edge": trafficResetter(func(context.Context, domain.TrafficSelection) (domain.TrafficCounterReset, error) {
			calls++
			return domain.TrafficCounterReset{PreviousBytes: 99}, cause
		}),
	})
	result, err := service.ResetTrafficCounter(t.Context(), "edge", domain.TrafficSelection{})
	if result != (domain.InstanceTrafficCounterReset{}) || !errors.Is(err, domain.ErrTrafficResetUnknown) || !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatalf("unknown effect changed or retried: %+v %v calls=%d", result, err, calls)
	}
}
