package services_test

import (
	"context"
	"sync"

	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/services"
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
	count  func(context.Context, string) (int64, error)
	get    func(context.Context, string, string) (*domain.Record, error)
	add    func(context.Context, string, []byte) (domain.NativeResult, error)
	remove func(context.Context, string, string) (domain.NativeResult, error)
}

func (c client) Count(ctx context.Context, tag string) (int64, error) { return c.count(ctx, tag) }
func (c client) Get(ctx context.Context, tag, email string) (*domain.Record, error) {
	return c.get(ctx, tag, email)
}

func (c client) Add(ctx context.Context, tag string, content []byte) (domain.NativeResult, error) {
	return c.add(ctx, tag, content)
}
func (c client) Remove(ctx context.Context, tag, email string) (domain.NativeResult, error) {
	return c.remove(ctx, tag, email)
}

type inboundReader struct{ status services.StatusReader }

func (r inboundReader) Get(ctx context.Context, id, tag string) (domain.Inbound, error) {
	state, err := r.status.Status(ctx, id)
	return domain.Inbound{Tag: tag, AccountType: "test.account", LiveAccounts: true, Generation: state.Execution.Generation}, err
}
func (r inboundReader) Select(ctx context.Context, id string, tags []string) ([]domain.Inbound, error) {
	result := make([]domain.Inbound, len(tags))
	for i, tag := range tags {
		var err error
		result[i], err = r.Get(ctx, id, tag)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

type accountInspector struct{}

func (accountInspector) AccountType([]byte) (string, error) { return "test.account", nil }
func newService(bindings []services.Binding, status services.StatusReader) (*services.Service, error) {
	return services.New(bindings, status, inboundReader{status}, accountInspector{})
}
