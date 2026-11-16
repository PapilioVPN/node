package services_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/core/routing/app/services"
	"github.com/PapilioVPN/node/internal/modules/core/routing/domain"
)

type routingReader struct {
	list     func(context.Context) (domain.RoutingRules, error)
	balancer func(context.Context, string) (domain.BalancerInfo, error)
}

func (r routingReader) ReadRoutingRules(ctx context.Context) (domain.RoutingRules, error) {
	return r.list(ctx)
}
func (r routingReader) ReadBalancerInfo(ctx context.Context, tag string) (domain.BalancerInfo, error) {
	return r.balancer(ctx, tag)
}

func TestRoutingReadSelectsOneInstanceAndPreservesRulePriorityAndBalancerPresence(t *testing.T) {
	ctx := t.Context()
	rules := domain.RoutingRules{Items: []domain.RoutingRuleReference{{RuleTag: "z-first", OutboundTag: "out-b"}, {RuleTag: "a-second", OutboundTag: "out-a"}, {}, {RuleTag: "z-first", OutboundTag: "out-b"}}, ObservedAt: time.UnixMilli(1700000000123)}
	empty := ""
	info := domain.BalancerInfo{OverrideTarget: &empty, PrincipalTargets: &domain.BalancerTargets{Tags: []string{"z", "a", "a"}}, ObservedAt: rules.ObservedAt}
	lists, balancers := 0, 0
	bindings := map[instances.InstanceID]services.RoutingReader{" edge ": routingReader{
		list: func(received context.Context) (domain.RoutingRules, error) {
			lists++
			if received != ctx {
				t.Fatal("list context changed")
			}
			return rules, nil
		},
		balancer: func(received context.Context, tag string) (domain.BalancerInfo, error) {
			balancers++
			if received != ctx || tag != " exact>>>pool " {
				t.Fatal("balancer context/tag changed")
			}
			return info, nil
		},
	}, "disabled": nil}
	service := services.NewRoutingService(bindings)
	delete(bindings, " edge ")
	result, err := service.ListRoutingRules(ctx, " edge ")
	if err != nil || result.InstanceID != " edge " || !reflect.DeepEqual(result.Rules, rules) || lists != 1 || balancers != 0 {
		t.Fatal("routing priority/order/duplicates were changed or over-fetched")
	}
	balancer, err := service.GetBalancerInfo(ctx, " edge ", " exact>>>pool ")
	if err != nil || balancer.InstanceID != " edge " || balancer.Tag != " exact>>>pool " || !reflect.DeepEqual(balancer.Balancer, info) || lists != 1 || balancers != 1 {
		t.Fatal("balancer presence/order or native selection changed")
	}
	for id, cause := range map[instances.InstanceID]error{"edge": instances.ErrNotFound, "disabled": domain.ErrAPIDisabled} {
		if _, err := service.ListRoutingRules(ctx, id); !errors.Is(err, cause) {
			t.Fatal("list crossed configured instance selection")
		}
		if _, err := service.GetBalancerInfo(ctx, id, "pool"); !errors.Is(err, cause) {
			t.Fatal("balancer crossed configured instance selection")
		}
	}
}

func TestRoutingErrorsOrLateCancellationReturnNoPartialObservations(t *testing.T) {
	for _, cause := range []error{domain.ErrRoutingReadUnsupported, domain.ErrAPIResourceLimit, errors.New("private diagnostics"), nil} {
		ctx, cancel := context.WithCancel(t.Context())
		service := services.NewRoutingService(map[instances.InstanceID]services.RoutingReader{"edge": routingReader{
			list: func(context.Context) (domain.RoutingRules, error) {
				if cause == nil {
					cancel()
				}
				return domain.RoutingRules{Items: []domain.RoutingRuleReference{{RuleTag: "partial"}}, ObservedAt: time.Now()}, cause
			},
			balancer: func(context.Context, string) (domain.BalancerInfo, error) {
				if cause == nil {
					cancel()
				}
				return domain.BalancerInfo{PrincipalTargets: &domain.BalancerTargets{Tags: []string{"partial"}}, ObservedAt: time.Now()}, cause
			},
		}})
		result, err := service.ListRoutingRules(ctx, "edge")
		if len(result.Rules.Items) != 0 || !result.Rules.ObservedAt.IsZero() || err == nil || cause != nil && !errors.Is(err, cause) || cause == nil && !errors.Is(err, context.Canceled) {
			t.Fatal("failed list returned partial facts")
		}
		cancel()
		ctx, cancel = context.WithCancel(t.Context())
		info, err := service.GetBalancerInfo(ctx, "edge", "pool")
		if info != (domain.InstanceBalancerInfo{}) || err == nil || cause != nil && !errors.Is(err, cause) || cause == nil && !errors.Is(err, context.Canceled) {
			t.Fatal("failed balancer read returned partial facts")
		}
		cancel()
		if _, err := service.ListRoutingRules(ctx, "edge"); !errors.Is(err, context.Canceled) {
			t.Fatal("pre-canceled routing read reached reader")
		}
	}
}
