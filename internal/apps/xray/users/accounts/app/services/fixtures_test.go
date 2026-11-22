package services_test

import (
	"context"
	"sync"

	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

type execution struct {
	mu         sync.Mutex
	generation uint64
	state      instances.State
}

func (e *execution) Status(ctx context.Context, id string) (instances.Status, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return instances.Status{ID: id, Execution: instances.Execution{State: e.state, Generation: e.generation}}, ctx.Err()
}
func (e *execution) replace() { e.mu.Lock(); defer e.mu.Unlock(); e.generation++ }

type client struct {
	count func(context.Context, string) (int64, error)
	get   func(context.Context, string, string) (*domain.Record, error)
}

func (c client) Count(ctx context.Context, tag string) (int64, error) { return c.count(ctx, tag) }
func (c client) Get(ctx context.Context, tag, email string) (*domain.Record, error) {
	return c.get(ctx, tag, email)
}
