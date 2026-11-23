package traffic

import (
	"context"
	"sync/atomic"
	"time"
)

const (
	readTimeout        = time.Second
	maxNameBytes       = 4096
	maxConcurrentReads = 128
	maxConcurrentLists = 16
)

type counters interface {
	Read(context.Context, string, Scope, string) (Usage, error)
	List(context.Context, string, Scope) ([]Usage, error)
	Reset(context.Context, Counter) (int64, error)
}

type Service struct {
	counters     counters
	reads        atomic.Int32
	resets       atomic.Int32
	resetEnabled bool
	lists        atomic.Int32
}

func New(counters counters, resetEnabled bool) *Service {
	return &Service{counters: counters, resetEnabled: resetEnabled}
}

func (service *Service) Get(ctx context.Context, id string, scope Scope, name string) (Usage, error) {
	if id == "" || !scope.valid() || name == "" || len(name) > maxNameBytes {
		return Usage{}, ErrInvalid
	}
	return withLimit(ctx, &service.reads, maxConcurrentReads, ErrBusy, func(ctx context.Context) (Usage, error) {
		return service.counters.Read(ctx, id, scope, name)
	})
}

func (service *Service) List(ctx context.Context, id string, scope Scope) ([]Usage, error) {
	if id == "" || !scope.valid() {
		return nil, ErrInvalid
	}
	return withLimit(ctx, &service.lists, maxConcurrentLists, ErrBusy, func(ctx context.Context) ([]Usage, error) {
		return service.counters.List(ctx, id, scope)
	})
}

func withLimit[T any](ctx context.Context, active *atomic.Int32, limit int32, busy error, read func(context.Context) (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	for count := active.Load(); ; count = active.Load() {
		if count >= limit {
			return zero, busy
		}
		if active.CompareAndSwap(count, count+1) {
			break
		}
	}
	defer active.Add(-1)

	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	return read(ctx)
}
