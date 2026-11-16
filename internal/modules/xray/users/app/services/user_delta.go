package services

import (
	"context"
	"maps"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/users/domain"
)

type InboundUserDeltaApplier interface {
	ApplyInboundUserDelta(context.Context, domain.InboundUserDelta) (domain.UserDeltaReport, error)
}

type UserDeltaBinding struct {
	Applier  InboundUserDeltaApplier
	MaxUsers uint32
}
type UserDeltaService struct {
	bindings map[instances.InstanceID]UserDeltaBinding
}

func NewUserDeltaService(bindings map[instances.InstanceID]UserDeltaBinding) *UserDeltaService {
	return &UserDeltaService{bindings: maps.Clone(bindings)}
}

// ApplyInboundUserDelta bounds requested identities before one native invocation.
// Native snapshots and progress keep their owned data, causes and completion time.
func (s *UserDeltaService) ApplyInboundUserDelta(ctx context.Context, id instances.InstanceID, input domain.InboundUserDelta) (domain.InstanceInboundUserDelta, error) {
	result := domain.InstanceInboundUserDelta{InstanceID: id, InboundTag: input.InboundTag}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	binding, exists := s.bindings[id]
	if !exists {
		return result, instances.ErrNotFound
	}
	if binding.Applier == nil {
		return result, domain.ErrAPIDisabled
	}
	if binding.MaxUsers == 0 {
		return result, domain.ErrUserCollectionsDisabled
	}
	if uint64(len(input.Users))+uint64(len(input.RemoveEmails)) > uint64(binding.MaxUsers) {
		return result, domain.ErrUserCollectionTooLarge
	}
	report, err := binding.Applier.ApplyInboundUserDelta(ctx, input)
	result.Report = report
	return result, err
}
