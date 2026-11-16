package services

import (
	"context"
	"maps"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/core/routing/domain"
)

// RoutingProber borrows the input synchronously and owns its result. Native
// effects can outlive cancellation; ErrRoutingProbeUnknown forbids blind replay.
type RoutingProber interface {
	TestRoute(context.Context, domain.RoutingProbe) (domain.RoutingProbeResult, error)
}

type RoutingProbeService struct {
	probers map[instances.InstanceID]RoutingProber
}

func NewRoutingProbeService(probers map[instances.InstanceID]RoutingProber) *RoutingProbeService {
	return &RoutingProbeService{probers: maps.Clone(probers)}
}

func (s *RoutingProbeService) TestRoute(ctx context.Context, id instances.InstanceID, probe domain.RoutingProbe) (domain.InstanceRoutingProbe, error) {
	if err := ctx.Err(); err != nil {
		return domain.InstanceRoutingProbe{}, err
	}
	prober, exists := s.probers[id]
	if !exists {
		return domain.InstanceRoutingProbe{}, instances.ErrNotFound
	}
	if prober == nil {
		return domain.InstanceRoutingProbe{}, domain.ErrAPIDisabled
	}
	result, err := prober.TestRoute(ctx, probe)
	if err != nil {
		return domain.InstanceRoutingProbe{}, err
	}
	// Completion has already been acknowledged; late cancellation cannot undo it.
	return domain.InstanceRoutingProbe{InstanceID: id, Result: result}, nil
}
