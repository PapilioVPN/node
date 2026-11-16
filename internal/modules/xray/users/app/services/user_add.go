package services

import (
	"context"
	"maps"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/users/domain"
)

// InboundUserAdder borrows validated credentials for one operation. Failures after
// invocation retain ErrUserMutationUnknown; native add/upsert semantics differ.
type InboundUserAdder interface {
	AddInboundUser(context.Context, domain.InboundUserAddition) (domain.UserMutationAcknowledgement, error)
}

type UserAdditionService struct {
	adders map[instances.InstanceID]InboundUserAdder
}

func NewUserAdditionService(adders map[instances.InstanceID]InboundUserAdder) *UserAdditionService {
	return &UserAdditionService{adders: maps.Clone(adders)}
}

func (s *UserAdditionService) AddInboundUser(ctx context.Context, id instances.InstanceID, addition domain.InboundUserAddition) (domain.InstanceInboundUserMutation, error) {
	if err := ctx.Err(); err != nil {
		return domain.InstanceInboundUserMutation{}, err
	}
	adder, exists := s.adders[id]
	if !exists {
		return domain.InstanceInboundUserMutation{}, instances.ErrNotFound
	}
	if adder == nil {
		return domain.InstanceInboundUserMutation{}, domain.ErrAPIDisabled
	}
	ack, err := adder.AddInboundUser(ctx, addition)
	if err != nil {
		return domain.InstanceInboundUserMutation{}, err
	}
	return domain.InstanceInboundUserMutation{InstanceID: id, Selection: domain.InboundUserSelection{InboundTag: addition.InboundTag, Email: addition.User.Email}, Acknowledgement: ack}, nil
}
