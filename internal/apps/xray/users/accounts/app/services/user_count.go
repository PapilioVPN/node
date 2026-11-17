package services

import (
	"context"
	"errors"
	"maps"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

// InboundUserCountReader counts native membership without fetching user records.
type InboundUserCountReader interface {
	ReadInboundUserCount(context.Context, string) (domain.InboundUserCount, error)
}

type UserCountService struct {
	readers map[instances.InstanceID]InboundUserCountReader
}

func NewUserCountService(readers map[instances.InstanceID]InboundUserCountReader) *UserCountService {
	return &UserCountService{readers: maps.Clone(readers)}
}

func (s *UserCountService) GetInboundUserCount(ctx context.Context, id instances.InstanceID, tag string) (domain.InstanceInboundUserCount, error) {
	if err := ctx.Err(); err != nil {
		return domain.InstanceInboundUserCount{}, err
	}
	reader, exists := s.readers[id]
	if !exists {
		return domain.InstanceInboundUserCount{}, instances.ErrNotFound
	}
	if reader == nil {
		return domain.InstanceInboundUserCount{}, domain.ErrAPIDisabled
	}
	count, err := reader.ReadInboundUserCount(ctx, tag)
	if cause := ctx.Err(); cause != nil {
		return domain.InstanceInboundUserCount{}, errors.Join(cause, err)
	}
	if err != nil {
		return domain.InstanceInboundUserCount{}, err
	}
	return domain.InstanceInboundUserCount{InstanceID: id, InboundTag: tag, Users: count}, nil
}
