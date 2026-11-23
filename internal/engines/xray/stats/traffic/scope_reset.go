package traffic

import (
	"context"
	"time"
)

const (
	MaxScopeResetCounters  = 1000
	MaxScopeResetNameBytes = 512 << 10
	scopeResetTimeout      = 5 * time.Second
)

type ScopeReset struct {
	ID          string
	Scope       Scope
	MaxCounters uint32
}

type CounterReset struct {
	Name      string
	Direction Direction
	DirectionReset
}

// ResetScope returns an ordered result for each selected counter. A nil
// report with an error means no per-counter effects could be reported.
func (service *Service) ResetScope(ctx context.Context, request ScopeReset) ([]CounterReset, error) {
	if !service.resetEnabled {
		return nil, ErrResetDisabled
	}
	if request.ID == "" || !request.Scope.Valid() || request.MaxCounters == 0 || request.MaxCounters > MaxScopeResetCounters {
		return nil, ErrInvalid
	}
	weight := min(service.scopeConcurrency, int32(request.MaxCounters))
	return withLimit(ctx, &service.resets, maxConcurrentResets, weight, scopeResetTimeout, ErrResetBusy, func(ctx context.Context) ([]CounterReset, error) {
		return service.counters.ResetScope(ctx, request, int(weight))
	})
}
