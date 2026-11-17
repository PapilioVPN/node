package services_test

import (
	"context"
	"errors"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
)

type balancerOverrider func(context.Context, domain.BalancerOverride) (domain.RoutingMutationAcknowledgement, error)

func (f balancerOverrider) OverrideBalancerTarget(ctx context.Context, selection domain.BalancerOverride) (domain.RoutingMutationAcknowledgement, error) {
	return f(ctx, selection)
}

func TestBalancerOverrideSelectsExactInstanceAndKeepsConfirmedLateAcknowledgement(t *testing.T) {
	for _, target := range []string{"", " outside>>>selector ", "   "} {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		selection, calls, ack := domain.BalancerOverride{BalancerTag: " exact>>>pool ", Target: target}, 0, domain.RoutingMutationAcknowledgement{AcknowledgedAt: time.Now()}
		bindings := map[instances.InstanceID]services.BalancerOverrider{" edge ": balancerOverrider(func(received context.Context, value domain.BalancerOverride) (domain.RoutingMutationAcknowledgement, error) {
			calls++
			if received != ctx || value != selection {
				t.Fatal("override context/selection/clear intent changed")
			}
			cancel()
			return ack, nil
		}), "disabled": nil}
		service := services.NewBalancerOverrideService(bindings)
		delete(bindings, " edge ")
		result, err := service.OverrideBalancerTarget(ctx, " edge ", selection)
		if err != nil || result.InstanceID != " edge " || result.Override != selection || result.Acknowledgement != ack || calls != 1 {
			t.Fatalf("confirmed override was lost/replayed: %+v %v", result, err)
		}
		if result, err := service.OverrideBalancerTarget(ctx, " edge ", selection); !errors.Is(err, context.Canceled) || errors.Is(err, domain.ErrRoutingMutationUnknown) || result != (domain.InstanceBalancerOverride{}) || calls != 1 {
			t.Fatalf("pre-canceled override reached effects: %+v %v", result, err)
		}
		for id, cause := range map[instances.InstanceID]error{"edge": instances.ErrNotFound, "disabled": domain.ErrAPIDisabled} {
			if _, err := service.OverrideBalancerTarget(t.Context(), id, selection); !errors.Is(err, cause) || errors.Is(err, domain.ErrRoutingMutationUnknown) || calls != 1 {
				t.Fatalf("override crossed feature/instance boundary: %v", err)
			}
		}
	}
}

func TestBalancerOverrideFailureRetainsCausesWithoutPartialAcknowledgement(t *testing.T) {
	for _, cause := range []error{domain.ErrRoutingManagementUnsupported, errors.Join(domain.ErrRoutingMutationUnknown, context.Canceled), errors.Join(domain.ErrRoutingMutationUnknown, domain.ErrAPIResourceLimit)} {
		calls := 0
		service := services.NewBalancerOverrideService(map[instances.InstanceID]services.BalancerOverrider{"edge": balancerOverrider(func(context.Context, domain.BalancerOverride) (domain.RoutingMutationAcknowledgement, error) {
			calls++
			return domain.RoutingMutationAcknowledgement{AcknowledgedAt: time.Now()}, cause
		})})
		result, err := service.OverrideBalancerTarget(t.Context(), "edge", domain.BalancerOverride{BalancerTag: "pool"})
		if !errors.Is(err, cause) || result != (domain.InstanceBalancerOverride{}) || calls != 1 {
			t.Fatalf("failed override returned confirmed facts/retried: %+v %v", result, err)
		}
	}
}
