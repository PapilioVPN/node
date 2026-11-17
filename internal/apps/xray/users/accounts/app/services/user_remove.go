package services

import (
	"context"
	"maps"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

// InboundUserRemover reports ErrUserMutationUnknown when an attempted effect
// lacks acknowledgement. A generic native error does not establish no effect.
type InboundUserRemover interface {
	RemoveInboundUser(context.Context, domain.InboundUserSelection) (domain.UserMutationAcknowledgement, error)
}

type UserRemovalService struct {
	removers map[instances.InstanceID]InboundUserRemover
}

func NewUserRemovalService(removers map[instances.InstanceID]InboundUserRemover) *UserRemovalService {
	return &UserRemovalService{removers: maps.Clone(removers)}
}

func (s *UserRemovalService) RemoveInboundUser(ctx context.Context, id instances.InstanceID, selection domain.InboundUserSelection) (domain.InstanceInboundUserMutation, error) {
	if err := ctx.Err(); err != nil {
		return domain.InstanceInboundUserMutation{}, err
	}
	remover, exists := s.removers[id]
	if !exists {
		return domain.InstanceInboundUserMutation{}, instances.ErrNotFound
	}
	if remover == nil {
		return domain.InstanceInboundUserMutation{}, domain.ErrAPIDisabled
	}
	acknowledgement, err := remover.RemoveInboundUser(ctx, selection)
	if err != nil {
		return domain.InstanceInboundUserMutation{}, err
	}
	return domain.InstanceInboundUserMutation{InstanceID: id, Selection: selection, Acknowledgement: acknowledgement}, nil
}
