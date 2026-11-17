package services

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"slices"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/network/outbounds/domain"
)

// OutboundStatusReader transfers a complete owned collection. The native API
// offers no exact-tag projection; both service operations use one collection RPC.
type OutboundStatusReader interface {
	ReadOutboundStatuses(context.Context) (domain.OutboundStatuses, error)
}

type OutboundStatusService struct {
	readers map[instances.InstanceID]OutboundStatusReader
}

func NewOutboundStatusService(readers map[instances.InstanceID]OutboundStatusReader) *OutboundStatusService {
	return &OutboundStatusService{readers: maps.Clone(readers)}
}

func (s *OutboundStatusService) read(ctx context.Context, id instances.InstanceID) (domain.OutboundStatuses, error) {
	if err := ctx.Err(); err != nil {
		return domain.OutboundStatuses{}, err
	}
	reader, exists := s.readers[id]
	if !exists {
		return domain.OutboundStatuses{}, instances.ErrNotFound
	}
	if reader == nil {
		return domain.OutboundStatuses{}, domain.ErrAPIDisabled
	}
	result, err := reader.ReadOutboundStatuses(ctx)
	if cause := ctx.Err(); cause != nil {
		return domain.OutboundStatuses{}, errors.Join(cause, err)
	}
	if err != nil {
		return domain.OutboundStatuses{}, err
	}
	return result, nil
}

func (s *OutboundStatusService) ListOutboundStatuses(ctx context.Context, id instances.InstanceID) (domain.InstanceOutboundStatuses, error) {
	result, err := s.read(ctx, id)
	if err != nil {
		return domain.InstanceOutboundStatuses{}, err
	}
	slices.SortStableFunc(result.Items, func(a, b domain.OutboundStatus) int { return cmp.Compare(a.Tag, b.Tag) })
	if err := ctx.Err(); err != nil {
		return domain.InstanceOutboundStatuses{}, err
	}
	return domain.InstanceOutboundStatuses{InstanceID: id, Statuses: result}, nil
}

// GetOutboundStatus selects one exact opaque tag, including an anonymous tag.
// Absence is a timestamped observation, not zero latency or missing membership.
func (s *OutboundStatusService) GetOutboundStatus(ctx context.Context, id instances.InstanceID, tag string) (domain.InstanceOutboundObservation, error) {
	result, err := s.read(ctx, id)
	if err != nil {
		return domain.InstanceOutboundObservation{}, err
	}
	observation := domain.OutboundObservation{ObservedAt: result.ObservedAt}
	for _, item := range result.Items {
		if err := ctx.Err(); err != nil {
			return domain.InstanceOutboundObservation{}, err
		}
		if item.Tag != tag {
			continue
		}
		if observation.Status != nil {
			return domain.InstanceOutboundObservation{}, domain.ErrInvalidOutboundObservation
		}
		// Retain only the selected record, not the full collection backing array.
		value := item
		observation.Status = &value
	}
	if err := ctx.Err(); err != nil {
		return domain.InstanceOutboundObservation{}, err
	}
	return domain.InstanceOutboundObservation{InstanceID: id, Tag: tag, Observation: observation}, nil
}
