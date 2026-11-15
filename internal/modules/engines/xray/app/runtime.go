package app

import (
	"context"
	"errors"
	"maps"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	instances "github.com/PapilioVPN/node/internal/modules/instances/domain"
)

type RuntimeInfoReader interface {
	ReadRuntimeInfo(context.Context) (domain.RuntimeInfo, error)
}

// RuntimeInfoService selects an immutable API binding without probing lifecycle.
// A nil reader represents a configured Xray instance without API settings.
type RuntimeInfoService struct {
	readers map[instances.InstanceID]RuntimeInfoReader
}

func NewRuntimeInfoService(readers map[instances.InstanceID]RuntimeInfoReader) *RuntimeInfoService {
	return &RuntimeInfoService{readers: maps.Clone(readers)}
}

func (s *RuntimeInfoService) GetRuntimeInfo(ctx context.Context, id instances.InstanceID) (domain.InstanceRuntimeInfo, error) {
	if err := ctx.Err(); err != nil {
		return domain.InstanceRuntimeInfo{}, err
	}
	reader, exists := s.readers[id]
	if !exists {
		return domain.InstanceRuntimeInfo{}, instances.ErrNotFound
	}
	if reader == nil {
		return domain.InstanceRuntimeInfo{}, domain.ErrAPIDisabled
	}
	info, err := reader.ReadRuntimeInfo(ctx)
	if cause := ctx.Err(); cause != nil {
		return domain.InstanceRuntimeInfo{}, errors.Join(cause, err)
	}
	if err != nil {
		return domain.InstanceRuntimeInfo{}, err
	}
	return domain.InstanceRuntimeInfo{InstanceID: id, Runtime: info}, nil
}
