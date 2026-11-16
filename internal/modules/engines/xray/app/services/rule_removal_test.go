package services_test

import (
	"context"
	"errors"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/app/services"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

type routingRuleRemover func(context.Context, string) (domain.RoutingMutationAcknowledgement, error)

func (f routingRuleRemover) RemoveRoutingRule(ctx context.Context, tag string) (domain.RoutingMutationAcknowledgement, error) {
	return f(ctx, tag)
}

func TestRuleRemovalSelectsExactInstanceAndPreservesConfirmedCompletion(t *testing.T) {
	for _, tag := range []string{" exact>>>rule ", "   ", "missing"} {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		calls, ack := 0, domain.RoutingMutationAcknowledgement{AcknowledgedAt: time.Now()}
		bindings := map[instances.InstanceID]services.RoutingRuleRemover{" edge ": routingRuleRemover(func(received context.Context, value string) (domain.RoutingMutationAcknowledgement, error) {
			calls++
			if received != ctx || value != tag {
				t.Fatal("removal context/tag changed")
			}
			cancel()
			return ack, nil
		}), "disabled": nil}
		service := services.NewRoutingRuleRemovalService(bindings)
		delete(bindings, " edge ")
		result, err := service.RemoveRoutingRule(ctx, " edge ", tag)
		if err != nil || result.InstanceID != " edge " || result.RuleTag != tag || result.Acknowledgement != ack || calls != 1 {
			t.Fatalf("confirmed removal changed/disappeared: %+v %v", result, err)
		}
		if _, err := service.RemoveRoutingRule(ctx, " edge ", tag); !errors.Is(err, context.Canceled) || errors.Is(err, domain.ErrRoutingMutationUnknown) || calls != 1 {
			t.Fatal("pre-canceled removal reached effects")
		}
		for id, cause := range map[instances.InstanceID]error{"edge": instances.ErrNotFound, "disabled": domain.ErrAPIDisabled} {
			if _, err := service.RemoveRoutingRule(t.Context(), id, tag); !errors.Is(err, cause) || errors.Is(err, domain.ErrRoutingMutationUnknown) || calls != 1 {
				t.Fatalf("removal crossed feature/instance boundary: %v", err)
			}
		}
	}
}

func TestRuleRemovalFailurePreservesCausesWithoutSuccessfulPartialResult(t *testing.T) {
	cause := errors.Join(domain.ErrRoutingMutationUnknown, context.Canceled)
	calls := 0
	service := services.NewRoutingRuleRemovalService(map[instances.InstanceID]services.RoutingRuleRemover{"edge": routingRuleRemover(func(context.Context, string) (domain.RoutingMutationAcknowledgement, error) {
		calls++
		return domain.RoutingMutationAcknowledgement{AcknowledgedAt: time.Now()}, cause
	})})
	result, err := service.RemoveRoutingRule(t.Context(), "edge", "rule")
	if !errors.Is(err, cause) || result != (domain.InstanceRoutingRuleRemoval{}) || calls != 1 {
		t.Fatalf("failed removal retried/returned partial acknowledgement: %+v %v", result, err)
	}
}
