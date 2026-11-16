package services

import (
	"context"
	"errors"
	"maps"
	"slices"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/traffic/domain"
)

// TrafficResetBinding shares existing native resources and bounds the entire
// discovery/reset operation, rather than granting a fresh budget to every item.
type TrafficResetBinding struct {
	MaxConcurrent int // Scope reset admission; zero retains sequential execution.
	Reader        TrafficCountersReader
	Resetter      TrafficCounterResetter
	Timeout       time.Duration
}

type TrafficCountersResetService struct {
	bindings map[instances.InstanceID]TrafficResetBinding
}

func NewTrafficCountersResetService(bindings map[instances.InstanceID]TrafficResetBinding) *TrafficCountersResetService {
	return &TrafficCountersResetService{bindings: maps.Clone(bindings)}
}

// ResetTrafficCounters uses one read and N exact resets, not native bulk. Xray's
// substring QueryStats(reset=true) cannot safely reset an exact scope. Preflight
// rejects excess/duplicate selections before effects. Stop new admission on the
// first observed failure, join admitted calls and retain their effects without
// retry. maxCounters is boundary-validated; omitted concurrency stays sequential.
func (s *TrafficCountersResetService) ResetTrafficCounters(ctx context.Context, id instances.InstanceID, scope domain.TrafficScope, maxCounters int) (domain.InstanceTrafficCountersReset, error) {
	result := domain.InstanceTrafficCountersReset{InstanceID: id, Scope: scope}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	binding, exists := s.bindings[id]
	if !exists {
		return result, instances.ErrNotFound
	}
	if binding.Reader == nil || binding.Resetter == nil {
		return result, domain.ErrAPIDisabled
	}
	ctx, cancel := context.WithTimeout(ctx, binding.Timeout)
	defer cancel()
	counters, err := binding.Reader.ReadTrafficCounters(ctx, scope)
	if err != nil || ctx.Err() != nil {
		return result, errors.Join(err, ctx.Err())
	}
	if len(counters.Items) > maxCounters {
		return result, domain.ErrAPIResourceLimit
	}
	slices.SortFunc(counters.Items, compareTrafficCounters)
	for i := 1; i < len(counters.Items); i++ {
		if compareTrafficCounters(counters.Items[i-1], counters.Items[i]) == 0 {
			// Native counters are unique. Replaying a duplicate could reset new usage.
			return result, domain.ErrInvalidTrafficResponse
		}
	}
	result.Reset.SelectedAt = counters.ObservedAt
	result.Reset.Items = make([]domain.TrafficCounterResetEntry, len(counters.Items))
	for i, item := range counters.Items {
		result.Reset.Items[i] = domain.TrafficCounterResetEntry{Name: item.Name, Direction: item.Direction, State: domain.TrafficResetNotAttempted}
	}
	if binding.MaxConcurrent > 1 && len(result.Reset.Items) > 1 {
		return result, resetConcurrentTrafficCounters(ctx, binding.Resetter, scope, result.Reset.Items, binding.MaxConcurrent)
	}
	for i := range result.Reset.Items {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		item := &result.Reset.Items[i]
		acknowledgement, err := binding.Resetter.ResetTrafficCounter(ctx, domain.TrafficSelection{Scope: scope, Name: item.Name, Direction: item.Direction})
		if err != nil {
			item.State = domain.TrafficResetNotApplied
			if errors.Is(err, domain.ErrTrafficResetUnknown) {
				item.State = domain.TrafficResetUnknown
			}
			return result, err
		}
		item.State, item.Reset = domain.TrafficResetAcknowledged, &acknowledgement
	}
	// Retain final acknowledgement even if cancellation races after its receipt.
	return result, nil
}
