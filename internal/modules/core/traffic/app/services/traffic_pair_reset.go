package services

import (
	"context"
	"errors"
	"maps"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/core/traffic/domain"
)

type TrafficPairResetBinding struct {
	Resetter TrafficCounterResetter
	Timeout  time.Duration
}

type TrafficPairResetService struct {
	bindings map[instances.InstanceID]TrafficPairResetBinding
}

func NewTrafficPairResetService(bindings map[instances.InstanceID]TrafficPairResetBinding) *TrafficPairResetService {
	return &TrafficPairResetService{bindings: maps.Clone(bindings)}
}

// ResetTrafficPair attempts uplink then downlink under one deadline, not atomically.
// Missing directions permit continuing. Stop on other failures, retain progress,
// and never retry; a lost report can hide even fully acknowledged effects.
func (s *TrafficPairResetService) ResetTrafficPair(ctx context.Context, id instances.InstanceID, selection domain.TrafficPairSelection) (domain.InstanceTrafficPairReset, error) {
	result := domain.InstanceTrafficPairReset{InstanceID: id, Selection: selection, Items: [2]domain.TrafficCounterResetEntry{
		{Name: selection.Name, Direction: domain.Uplink, State: domain.TrafficResetNotAttempted},
		{Name: selection.Name, Direction: domain.Downlink, State: domain.TrafficResetNotAttempted},
	}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	binding, exists := s.bindings[id]
	if !exists {
		return result, instances.ErrNotFound
	}
	if binding.Resetter == nil {
		return result, domain.ErrAPIDisabled
	}
	ctx, cancel := context.WithTimeout(ctx, binding.Timeout)
	defer cancel()
	for i := range result.Items {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		item := &result.Items[i]
		acknowledgement, err := binding.Resetter.ResetTrafficCounter(ctx, domain.TrafficSelection{Scope: selection.Scope, Name: selection.Name, Direction: item.Direction})
		if errors.Is(err, domain.ErrTrafficCounterNotFound) && !errors.Is(err, domain.ErrTrafficResetUnknown) {
			item.State = domain.TrafficResetMissing
			continue
		}
		if err != nil {
			item.State = domain.TrafficResetNotApplied
			if errors.Is(err, domain.ErrTrafficResetUnknown) {
				item.State = domain.TrafficResetUnknown
			}
			return result, err
		}
		item.State, item.Reset = domain.TrafficResetAcknowledged, &acknowledgement
	}
	return result, nil
}
