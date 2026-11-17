package services

import (
	"context"
	"maps"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

// InboundUsersAdder retains its ordered progress on failure as well as success.
type InboundUsersAdder interface {
	AddInboundUsers(context.Context, domain.InboundUserAdditions) (domain.UserAdditionsReport, error)
}

type UserAdditionsBinding struct {
	Adder    InboundUsersAdder
	MaxUsers uint32
}

type UserAdditionsService struct {
	bindings map[instances.InstanceID]UserAdditionsBinding
}

func NewUserAdditionsService(bindings map[instances.InstanceID]UserAdditionsBinding) *UserAdditionsService {
	return &UserAdditionsService{bindings: maps.Clone(bindings)}
}

func (s *UserAdditionsService) AddInboundUsers(ctx context.Context, id instances.InstanceID, input domain.InboundUserAdditions) (domain.InstanceInboundUserAdditions, error) {
	result := domain.InstanceInboundUserAdditions{InstanceID: id, InboundTag: input.InboundTag}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	binding, exists := s.bindings[id]
	if !exists {
		return result, instances.ErrNotFound
	}
	if binding.Adder == nil {
		return result, domain.ErrAPIDisabled
	}
	if binding.MaxUsers == 0 {
		return result, domain.ErrUserCollectionsDisabled
	}
	if uint64(len(input.Users)) > uint64(binding.MaxUsers) {
		return result, domain.ErrUserCollectionTooLarge
	}
	report, err := binding.Adder.AddInboundUsers(ctx, input)
	result.Report = report
	return result, err
}
