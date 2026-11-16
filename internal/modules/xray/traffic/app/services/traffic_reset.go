package services

import (
	"context"
	"maps"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/traffic/domain"
)

// TrafficCounterResetter performs one boundary-validated reset. A missing
// acknowledgement after an attempted effect must retain ErrTrafficResetUnknown.
type TrafficCounterResetter interface {
	ResetTrafficCounter(context.Context, domain.TrafficSelection) (domain.TrafficCounterReset, error)
}

type TrafficCounterResetService struct {
	resetters map[instances.InstanceID]TrafficCounterResetter
}

func NewTrafficCounterResetService(resetters map[instances.InstanceID]TrafficCounterResetter) *TrafficCounterResetService {
	return &TrafficCounterResetService{resetters: maps.Clone(resetters)}
}

func (s *TrafficCounterResetService) ResetTrafficCounter(ctx context.Context, id instances.InstanceID, selection domain.TrafficSelection) (domain.InstanceTrafficCounterReset, error) {
	if err := ctx.Err(); err != nil {
		return domain.InstanceTrafficCounterReset{}, err
	}
	resetter, exists := s.resetters[id]
	if !exists {
		return domain.InstanceTrafficCounterReset{}, instances.ErrNotFound
	}
	if resetter == nil {
		return domain.InstanceTrafficCounterReset{}, domain.ErrAPIDisabled
	}
	result, err := resetter.ResetTrafficCounter(ctx, selection)
	if err != nil {
		return domain.InstanceTrafficCounterReset{}, err
	}
	// A confirmed effect is not turned into "not applied" by a late cancellation.
	return domain.InstanceTrafficCounterReset{InstanceID: id, Selection: selection, Reset: result}, nil
}
