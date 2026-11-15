package services

import (
	"context"
	"errors"
	"maps"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

// RoutingReader offers distinct native observations without mutations or probes.
// Successful results transfer ownership; rule order and field presence are facts.
type RoutingReader interface {
	ReadRoutingRules(context.Context) (domain.RoutingRules, error)
	ReadBalancerInfo(context.Context, string) (domain.BalancerInfo, error)
}

type RoutingService struct {
	readers map[instances.InstanceID]RoutingReader
}

func NewRoutingService(readers map[instances.InstanceID]RoutingReader) *RoutingService {
	return &RoutingService{readers: maps.Clone(readers)}
}

func (s *RoutingService) reader(ctx context.Context, id instances.InstanceID) (RoutingReader, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reader, exists := s.readers[id]
	if !exists {
		return nil, instances.ErrNotFound
	}
	if reader == nil {
		return nil, domain.ErrAPIDisabled
	}
	return reader, nil
}

func (s *RoutingService) ListRoutingRules(ctx context.Context, id instances.InstanceID) (domain.InstanceRoutingRules, error) {
	reader, err := s.reader(ctx, id)
	if err != nil {
		return domain.InstanceRoutingRules{}, err
	}
	rules, err := reader.ReadRoutingRules(ctx)
	if cause := ctx.Err(); cause != nil {
		return domain.InstanceRoutingRules{}, errors.Join(cause, err)
	}
	if err != nil {
		return domain.InstanceRoutingRules{}, err
	}
	return domain.InstanceRoutingRules{InstanceID: id, Rules: rules}, nil
}

func (s *RoutingService) GetBalancerInfo(ctx context.Context, id instances.InstanceID, tag string) (domain.InstanceBalancerInfo, error) {
	reader, err := s.reader(ctx, id)
	if err != nil {
		return domain.InstanceBalancerInfo{}, err
	}
	info, err := reader.ReadBalancerInfo(ctx, tag)
	if cause := ctx.Err(); cause != nil {
		return domain.InstanceBalancerInfo{}, errors.Join(cause, err)
	}
	if err != nil {
		return domain.InstanceBalancerInfo{}, err
	}
	return domain.InstanceBalancerInfo{InstanceID: id, Tag: tag, Balancer: info}, nil
}
