package services

import (
	"context"
	"errors"
	"maps"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/core/traffic/domain"
)

type TrafficPairReadBinding struct {
	Reader  TrafficCounterReader
	Timeout time.Duration
}

type TrafficPairService struct {
	bindings map[instances.InstanceID]TrafficPairReadBinding
}

func NewTrafficPairService(bindings map[instances.InstanceID]TrafficPairReadBinding) *TrafficPairService {
	return &TrafficPairService{bindings: maps.Clone(bindings)}
}

// GetTrafficPair uses two exact reads under one deadline. Missing directions
// remain absent; any other failure discards partial observations. No native scan.
func (s *TrafficPairService) GetTrafficPair(ctx context.Context, id instances.InstanceID, selection domain.TrafficPairSelection) (domain.InstanceTrafficPair, error) {
	if err := ctx.Err(); err != nil {
		return domain.InstanceTrafficPair{}, err
	}
	binding, exists := s.bindings[id]
	if !exists {
		return domain.InstanceTrafficPair{}, instances.ErrNotFound
	}
	if binding.Reader == nil {
		return domain.InstanceTrafficPair{}, domain.ErrAPIDisabled
	}
	ctx, cancel := context.WithTimeout(ctx, binding.Timeout)
	defer cancel()
	result := domain.InstanceTrafficPair{InstanceID: id, Selection: selection}
	for _, direction := range [2]domain.TrafficDirection{domain.Uplink, domain.Downlink} {
		counter, err := binding.Reader.ReadTrafficCounter(ctx, domain.TrafficSelection{Scope: selection.Scope, Name: selection.Name, Direction: direction})
		if ctx.Err() != nil {
			return domain.InstanceTrafficPair{}, errors.Join(err, ctx.Err())
		}
		if errors.Is(err, domain.ErrTrafficCounterNotFound) {
			continue
		}
		if err != nil {
			return domain.InstanceTrafficPair{}, err
		}
		if direction == domain.Uplink {
			result.Pair.Uplink = &counter
		} else {
			result.Pair.Downlink = &counter
		}
	}
	return result, nil
}
