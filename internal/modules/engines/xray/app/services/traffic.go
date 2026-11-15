package services

import (
	"context"
	"errors"
	"maps"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

// TrafficCounterReader reads a boundary-validated selection without resetting it.
type TrafficCounterReader interface {
	ReadTrafficCounter(context.Context, domain.TrafficSelection) (domain.TrafficCounter, error)
}

type TrafficCounterService struct {
	readers map[instances.InstanceID]TrafficCounterReader
}

func NewTrafficCounterService(readers map[instances.InstanceID]TrafficCounterReader) *TrafficCounterService {
	return &TrafficCounterService{readers: maps.Clone(readers)}
}

func (s *TrafficCounterService) GetTrafficCounter(ctx context.Context, id instances.InstanceID, selection domain.TrafficSelection) (domain.InstanceTrafficCounter, error) {
	if err := ctx.Err(); err != nil {
		return domain.InstanceTrafficCounter{}, err
	}
	reader, exists := s.readers[id]
	if !exists {
		return domain.InstanceTrafficCounter{}, instances.ErrNotFound
	}
	if reader == nil {
		return domain.InstanceTrafficCounter{}, domain.ErrAPIDisabled
	}
	counter, err := reader.ReadTrafficCounter(ctx, selection)
	if cause := ctx.Err(); cause != nil {
		return domain.InstanceTrafficCounter{}, errors.Join(cause, err)
	}
	if err != nil {
		return domain.InstanceTrafficCounter{}, err
	}
	return domain.InstanceTrafficCounter{InstanceID: id, Selection: selection, Counter: counter}, nil
}
