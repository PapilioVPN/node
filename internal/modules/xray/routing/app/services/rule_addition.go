package services

import (
	"context"
	"errors"
	"maps"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/routing/app/validators"
	"github.com/PapilioVPN/node/internal/modules/xray/routing/domain"
)

type RoutingRulePreparer interface {
	PrepareRoutingRule(context.Context, []byte) (domain.PreparedRoutingRule, error)
}

type RoutingRuleApplier interface {
	ApplyRoutingRule(context.Context, domain.PreparedRoutingRule, domain.RoutingRuleMode) (domain.RoutingMutationAcknowledgement, error)
}

type RoutingRuleBinding struct {
	Preparer RoutingRulePreparer
	Applier  RoutingRuleApplier
	Timeout  time.Duration
}

type RoutingRuleAdditionService struct {
	bindings map[instances.InstanceID]RoutingRuleBinding
}

func NewRoutingRuleAdditionService(bindings map[instances.InstanceID]RoutingRuleBinding) *RoutingRuleAdditionService {
	return &RoutingRuleAdditionService{bindings: maps.Clone(bindings)}
}

// AddRoutingRule prepares before applying. Cleanup failures prevent dispatch;
// after dispatch, native effects and valid acknowledgements remain authoritative.
func (s *RoutingRuleAdditionService) AddRoutingRule(ctx context.Context, id instances.InstanceID, input domain.RoutingRuleAddition) (domain.InstanceRoutingRuleAddition, error) {
	if err := ctx.Err(); err != nil {
		return domain.InstanceRoutingRuleAddition{}, err
	}
	binding, ok := s.bindings[id]
	if !ok {
		return domain.InstanceRoutingRuleAddition{}, instances.ErrNotFound
	}
	if binding.Applier == nil {
		return domain.InstanceRoutingRuleAddition{}, domain.ErrAPIDisabled
	}
	if binding.Preparer == nil || binding.Timeout <= 0 {
		return domain.InstanceRoutingRuleAddition{}, domain.ErrRoutingRulePreparationDisabled
	}
	if err := validators.RoutingRuleMode(input.Mode); err != nil {
		return domain.InstanceRoutingRuleAddition{}, err
	}
	if len(input.JSON) == 0 {
		return domain.InstanceRoutingRuleAddition{}, domain.ErrInvalidRoutingRule
	}
	ctx, cancel := context.WithTimeout(ctx, binding.Timeout)
	defer cancel()
	prepared, err := binding.Preparer.PrepareRoutingRule(ctx, input.JSON)
	if err != nil {
		return domain.InstanceRoutingRuleAddition{}, errors.Join(domain.ErrRoutingRulePreparationFailed, err)
	}
	if err := ctx.Err(); err != nil {
		return domain.InstanceRoutingRuleAddition{}, err
	}
	ack, err := binding.Applier.ApplyRoutingRule(ctx, prepared, input.Mode)
	if err != nil {
		return domain.InstanceRoutingRuleAddition{}, err
	}
	return domain.InstanceRoutingRuleAddition{InstanceID: id, RuleTag: prepared.RuleTag, Mode: input.Mode, Acknowledgement: ack}, nil
}
