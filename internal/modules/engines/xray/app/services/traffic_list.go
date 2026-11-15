package services

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"slices"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

// TrafficCountersReader performs one non-reset collection read. Success transfers
// ownership of its items to the caller; failure must not become a partial success.
type TrafficCountersReader interface {
	ReadTrafficCounters(context.Context, domain.TrafficScope) (domain.TrafficCounters, error)
}

type TrafficCountersService struct {
	readers map[instances.InstanceID]TrafficCountersReader
}

func NewTrafficCountersService(readers map[instances.InstanceID]TrafficCountersReader) *TrafficCountersService {
	return &TrafficCountersService{readers: maps.Clone(readers)}
}

// ListTrafficCounters orders native observations by exact name then direction,
// preserving duplicates and their relative order for identical keys.
func (s *TrafficCountersService) ListTrafficCounters(ctx context.Context, id instances.InstanceID, scope domain.TrafficScope) (domain.InstanceTrafficCounters, error) {
	if err := ctx.Err(); err != nil {
		return domain.InstanceTrafficCounters{}, err
	}
	reader, exists := s.readers[id]
	if !exists {
		return domain.InstanceTrafficCounters{}, instances.ErrNotFound
	}
	if reader == nil {
		return domain.InstanceTrafficCounters{}, domain.ErrAPIDisabled
	}
	counters, err := reader.ReadTrafficCounters(ctx, scope)
	if cause := ctx.Err(); cause != nil {
		return domain.InstanceTrafficCounters{}, errors.Join(cause, err)
	}
	if err != nil {
		return domain.InstanceTrafficCounters{}, err
	}
	slices.SortStableFunc(counters.Items, compareTrafficCounters)
	if err := ctx.Err(); err != nil {
		return domain.InstanceTrafficCounters{}, err
	}
	return domain.InstanceTrafficCounters{InstanceID: id, Scope: scope, Counters: counters}, nil
}

func compareTrafficCounters(a, b domain.TrafficCounterEntry) int {
	if byName := cmp.Compare(a.Name, b.Name); byName != 0 {
		return byName
	}
	return cmp.Compare(a.Direction, b.Direction)
}
