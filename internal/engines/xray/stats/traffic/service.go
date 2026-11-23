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

type reader interface {
	Read(context.Context, string, Scope, string) (Usage, error)
	List(context.Context, string, Scope) ([]Usage, error)
}

type Service struct {
	reader reader
	reads  atomic.Int32
	lists  atomic.Int32
}

func New(reader reader) *Service {
	return &Service{reader: reader}
}

func (service *Service) Get(ctx context.Context, id string, scope Scope, name string) (Usage, error) {
	if id == "" || !scope.valid() || name == "" || len(name) > maxNameBytes {
		return Usage{}, ErrInvalid
	}
	return withReadLimit(ctx, &service.reads, maxConcurrentReads, func(ctx context.Context) (Usage, error) {
		return service.reader.Read(ctx, id, scope, name)
	})
}

func (service *Service) List(ctx context.Context, id string, scope Scope) ([]Usage, error) {
	if id == "" || !scope.valid() {
		return nil, ErrInvalid
	}
	return withReadLimit(ctx, &service.lists, maxConcurrentLists, func(ctx context.Context) ([]Usage, error) {
		return service.reader.List(ctx, id, scope)
	})
}

func withReadLimit[T any](ctx context.Context, active *atomic.Int32, limit int32, read func(context.Context) (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	for count := active.Load(); ; count = active.Load() {
		if count >= limit {
			return zero, ErrBusy
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
