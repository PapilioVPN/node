package services

import (
	"context"
	"maps"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

type RoutingRuleRemover interface {
	RemoveRoutingRule(context.Context, string) (domain.RoutingMutationAcknowledgement, error)
}

type RoutingRuleRemovalService struct {
	removers map[instances.InstanceID]RoutingRuleRemover
}

func NewRoutingRuleRemovalService(removers map[instances.InstanceID]RoutingRuleRemover) *RoutingRuleRemovalService {
	return &RoutingRuleRemovalService{removers: maps.Clone(removers)}
}

func (s *RoutingRuleRemovalService) RemoveRoutingRule(ctx context.Context, id instances.InstanceID, tag string) (domain.InstanceRoutingRuleRemoval, error) {
	if err := ctx.Err(); err != nil {
		return domain.InstanceRoutingRuleRemoval{}, err
	}
	remover, exists := s.removers[id]
	if !exists {
		return domain.InstanceRoutingRuleRemoval{}, instances.ErrNotFound
	}
	if remover == nil {
		return domain.InstanceRoutingRuleRemoval{}, domain.ErrAPIDisabled
	}
	ack, err := remover.RemoveRoutingRule(ctx, tag)
	if err != nil {
		return domain.InstanceRoutingRuleRemoval{}, err
	}
	return domain.InstanceRoutingRuleRemoval{InstanceID: id, RuleTag: tag, Acknowledgement: ack}, nil
}
