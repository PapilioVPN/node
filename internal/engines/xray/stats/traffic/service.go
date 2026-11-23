package traffic

import (
	"context"
	"sync/atomic"
	"time"
)

const (
	readTimeout         = time.Second
	MaxCounterNameBytes = 4096
	maxConcurrentReads  = 128
	maxConcurrentLists  = 16
)

type counters interface {
	Read(context.Context, string, Scope, string) (Usage, error)
	List(context.Context, string, Scope) ([]Usage, error)
	Reset(context.Context, Counter) (int64, error)
	ResetPair(context.Context, CounterPair) (PairReset, error)
	ResetScope(context.Context, ScopeReset, int) ([]CounterReset, error)
}

type Service struct {
	counters         counters
	reads            atomic.Int32
	resets           atomic.Int32
	resetEnabled     bool
	scopeConcurrency int32
	lists            atomic.Int32
}

func New(counters counters, resetEnabled bool, scopeConcurrency uint32) *Service {
	return &Service{
		counters: counters, resetEnabled: resetEnabled,
		scopeConcurrency: int32(min(max(scopeConcurrency, 1), maxConcurrentResets)),
	}
}

func (service *Service) Get(ctx context.Context, id string, scope Scope, name string) (Usage, error) {
	if id == "" || !scope.Valid() || name == "" || len(name) > MaxCounterNameBytes {
		return Usage{}, ErrInvalid
	}
	return withLimit(ctx, &service.reads, maxConcurrentReads, 1, readTimeout, ErrBusy, func(ctx context.Context) (Usage, error) {
		return service.counters.Read(ctx, id, scope, name)
	})
}

func (service *Service) List(ctx context.Context, id string, scope Scope) ([]Usage, error) {
	if id == "" || !scope.Valid() {
		return nil, ErrInvalid
	}
	return withLimit(ctx, &service.lists, maxConcurrentLists, 1, readTimeout, ErrBusy, func(ctx context.Context) ([]Usage, error) {
		return service.counters.List(ctx, id, scope)
	})
}

func withLimit[T any](ctx context.Context, active *atomic.Int32, limit, weight int32, timeout time.Duration, busy error, run func(context.Context) (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	for count := active.Load(); ; count = active.Load() {
		if count+weight > limit {
			return zero, busy
		}
		if active.CompareAndSwap(count, count+weight) {
			break
		}
	}
	defer active.Add(-weight)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return run(ctx)
}
