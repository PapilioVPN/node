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

type rulePreparer func(context.Context, []byte) (domain.PreparedRoutingRule, error)

func (f rulePreparer) PrepareRoutingRule(ctx context.Context, input []byte) (domain.PreparedRoutingRule, error) {
	return f(ctx, input)
}

type ruleApplier func(context.Context, domain.PreparedRoutingRule, domain.RoutingRuleMode) (domain.RoutingMutationAcknowledgement, error)

func (f ruleApplier) ApplyRoutingRule(ctx context.Context, input domain.PreparedRoutingRule, mode domain.RoutingRuleMode) (domain.RoutingMutationAcknowledgement, error) {
	return f(ctx, input, mode)
}

func TestRuleAdditionPreparesSelectedInstanceOnceAndRetainsLateCompletion(t *testing.T) {
	for _, mode := range []domain.RoutingRuleMode{domain.AppendRoutingRule, domain.ReplaceRoutingRules} {
		parent, cancel := context.WithCancel(t.Context())
		defer cancel()
		input := []byte(`{"native":"opaque"}`)
		compiled := []byte("native-owned-config")
		preparedCalls, appliedCalls := 0, 0
		var operationContext context.Context
		ack := domain.RoutingMutationAcknowledgement{AcknowledgedAt: time.Now()}
		bindings := map[instances.InstanceID]services.RoutingRuleBinding{" edge ": {
			Timeout: time.Second,
			Preparer: rulePreparer(func(ctx context.Context, content []byte) (domain.PreparedRoutingRule, error) {
				preparedCalls++
				operationContext = ctx
				if &content[0] != &input[0] {
					t.Fatal("borrowed native input copied/reencoded")
				}
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > time.Second {
					t.Fatal("preparation has no operation deadline")
				}
				return domain.PreparedRoutingRule{Config: compiled, RuleTag: ""}, nil
			}),
			Applier: ruleApplier(func(ctx context.Context, prepared domain.PreparedRoutingRule, received domain.RoutingRuleMode) (domain.RoutingMutationAcknowledgement, error) {
				appliedCalls++
				if ctx != operationContext || &prepared.Config[0] != &compiled[0] || prepared.RuleTag != "" || received != mode {
					t.Fatal("stage context/native payload/mode changed")
				}
				cancel()
				return ack, nil
			}),
		}}
		service := services.NewRoutingRuleAdditionService(bindings)
		delete(bindings, " edge ")
		result, err := service.AddRoutingRule(parent, " edge ", domain.RoutingRuleAddition{JSON: input, Mode: mode})
		if err != nil || result.InstanceID != " edge " || result.RuleTag != "" || result.Mode != mode || result.Acknowledgement != ack || preparedCalls != 1 || appliedCalls != 1 {
			t.Fatalf("confirmed native addition changed/disappeared: %+v %v", result, err)
		}
	}
}

func TestRuleAdditionRejectsBeforePreparationAndNeverCrossesInstance(t *testing.T) {
	calls := 0
	preparer := rulePreparer(func(context.Context, []byte) (domain.PreparedRoutingRule, error) {
		calls++
		return domain.PreparedRoutingRule{}, nil
	})
	applier := ruleApplier(func(context.Context, domain.PreparedRoutingRule, domain.RoutingRuleMode) (domain.RoutingMutationAcknowledgement, error) {
		calls++
		return domain.RoutingMutationAcknowledgement{}, nil
	})
	service := services.NewRoutingRuleAdditionService(map[instances.InstanceID]services.RoutingRuleBinding{
		"exact": {Preparer: preparer, Applier: applier, Timeout: time.Second}, "api-disabled": {}, "preparation-disabled": {Applier: applier, Timeout: time.Second}, "no-budget": {Preparer: preparer, Applier: applier},
	})
	valid := domain.RoutingRuleAddition{JSON: []byte("{}"), Mode: domain.AppendRoutingRule}
	for id, cause := range map[instances.InstanceID]error{" exact ": instances.ErrNotFound, "api-disabled": domain.ErrAPIDisabled, "preparation-disabled": domain.ErrRoutingRulePreparationDisabled, "no-budget": domain.ErrRoutingRulePreparationDisabled} {
		if _, err := service.AddRoutingRule(t.Context(), id, valid); !errors.Is(err, cause) {
			t.Fatalf("selection/bounds failure lost: %v", err)
		}
	}
	for _, input := range []domain.RoutingRuleAddition{{JSON: valid.JSON}, {JSON: valid.JSON, Mode: 255}, {Mode: domain.AppendRoutingRule}} {
		if result, err := service.AddRoutingRule(t.Context(), "exact", input); err == nil || errors.Is(err, domain.ErrRoutingMutationUnknown) || result != (domain.InstanceRoutingRuleAddition{}) {
			t.Fatalf("invalid selection reached stages: %+v %v", result, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.AddRoutingRule(ctx, "exact", valid); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("rejected operation ran a preparation/application stage")
	}
}

func TestRuleAdditionPreparationFailureAndBetweenStageCancellationDoNotApply(t *testing.T) {
	for _, cause := range []error{domain.ErrInvalidRoutingRule, domain.ErrRoutingRuleInputLimit, domain.ErrRoutingRuleOutputLimit, errors.New("private filesystem cleanup")} {
		applied := 0
		service := services.NewRoutingRuleAdditionService(map[instances.InstanceID]services.RoutingRuleBinding{"edge": {
			Timeout: time.Second, Preparer: rulePreparer(func(context.Context, []byte) (domain.PreparedRoutingRule, error) {
				return domain.PreparedRoutingRule{Config: []byte("partial")}, cause
			}),
			Applier: ruleApplier(func(context.Context, domain.PreparedRoutingRule, domain.RoutingRuleMode) (domain.RoutingMutationAcknowledgement, error) {
				applied++
				return domain.RoutingMutationAcknowledgement{}, nil
			}),
		}})
		result, err := service.AddRoutingRule(t.Context(), "edge", domain.RoutingRuleAddition{JSON: []byte("{}"), Mode: domain.AppendRoutingRule})
		if !errors.Is(err, cause) || !errors.Is(err, domain.ErrRoutingRulePreparationFailed) || errors.Is(err, domain.ErrRoutingMutationUnknown) || result != (domain.InstanceRoutingRuleAddition{}) || applied != 0 {
			t.Fatalf("failed preparation reached mutation or lost provenance: %+v %v", result, err)
		}
	}
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	service := services.NewRoutingRuleAdditionService(map[instances.InstanceID]services.RoutingRuleBinding{"edge": {
		Timeout: time.Second, Preparer: rulePreparer(func(context.Context, []byte) (domain.PreparedRoutingRule, error) {
			cancel()
			return domain.PreparedRoutingRule{Config: []byte("prepared")}, nil
		}),
		Applier: ruleApplier(func(context.Context, domain.PreparedRoutingRule, domain.RoutingRuleMode) (domain.RoutingMutationAcknowledgement, error) {
			t.Fatal("canceled handoff dispatched mutation")
			return domain.RoutingMutationAcknowledgement{}, nil
		}),
	}})
	if _, err := service.AddRoutingRule(parent, "edge", domain.RoutingRuleAddition{JSON: []byte("{}"), Mode: domain.ReplaceRoutingRules}); !errors.Is(err, context.Canceled) || errors.Is(err, domain.ErrRoutingMutationUnknown) {
		t.Fatal(err)
	}
}

func TestRuleAdditionApplicationErrorIsNotMisreportedAsPreparationFailure(t *testing.T) {
	calls := 0
	cause := errors.Join(domain.ErrRoutingMutationUnknown, context.DeadlineExceeded)
	service := services.NewRoutingRuleAdditionService(map[instances.InstanceID]services.RoutingRuleBinding{"edge": {
		Timeout: time.Second, Preparer: rulePreparer(func(context.Context, []byte) (domain.PreparedRoutingRule, error) {
			return domain.PreparedRoutingRule{Config: []byte("prepared")}, nil
		}),
		Applier: ruleApplier(func(context.Context, domain.PreparedRoutingRule, domain.RoutingRuleMode) (domain.RoutingMutationAcknowledgement, error) {
			calls++
			return domain.RoutingMutationAcknowledgement{AcknowledgedAt: time.Now()}, cause
		}),
	}})
	result, err := service.AddRoutingRule(t.Context(), "edge", domain.RoutingRuleAddition{JSON: []byte("{}"), Mode: domain.ReplaceRoutingRules})
	if !errors.Is(err, cause) || errors.Is(err, domain.ErrRoutingRulePreparationFailed) || result != (domain.InstanceRoutingRuleAddition{}) || calls != 1 {
		t.Fatalf("unknown application lost effects/retried/returned partial acknowledgement: %+v %v", result, err)
	}
}
