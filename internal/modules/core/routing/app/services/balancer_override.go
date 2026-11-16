package services

import (
	"context"
	"maps"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/core/routing/domain"
)

type BalancerOverrider interface {
	OverrideBalancerTarget(context.Context, domain.BalancerOverride) (domain.RoutingMutationAcknowledgement, error)
}

type BalancerOverrideService struct {
	overriders map[instances.InstanceID]BalancerOverrider
}

func NewBalancerOverrideService(overriders map[instances.InstanceID]BalancerOverrider) *BalancerOverrideService {
	return &BalancerOverrideService{overriders: maps.Clone(overriders)}
}

func (s *BalancerOverrideService) OverrideBalancerTarget(ctx context.Context, id instances.InstanceID, selection domain.BalancerOverride) (domain.InstanceBalancerOverride, error) {
	if err := ctx.Err(); err != nil {
		return domain.InstanceBalancerOverride{}, err
	}
	overrider, exists := s.overriders[id]
	if !exists {
		return domain.InstanceBalancerOverride{}, instances.ErrNotFound
	}
	if overrider == nil {
		return domain.InstanceBalancerOverride{}, domain.ErrAPIDisabled
	}
	ack, err := overrider.OverrideBalancerTarget(ctx, selection)
	if err != nil {
		return domain.InstanceBalancerOverride{}, err
	}
	return domain.InstanceBalancerOverride{InstanceID: id, Override: selection, Acknowledgement: ack}, nil
}
