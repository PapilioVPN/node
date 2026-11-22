package services_test

import (
	"context"
	"sync/atomic"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type state struct{ generation atomic.Uint64 }

func (s *state) Status(context.Context, string) (instances.Status, error) {
	return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: s.generation.Load()}}, nil
}

type backend struct {
	rows          []domain.Rule
	lists, writes int
	invoke        func() (domain.NativeResult, error)
}

func (b *backend) ListRules(context.Context) ([]domain.Rule, error) { b.lists++; return b.rows, nil }
func (b *backend) Balancer(context.Context, string) (domain.Balancer, error) {
	return domain.Balancer{}, nil
}
func (b *backend) call() (domain.NativeResult, error) {
	b.writes++
	if b.invoke != nil {
		return b.invoke()
	}
	return domain.NativeResult{Effect: domain.Completed, Acknowledged: true}, nil
}
func (b *backend) Add(context.Context, []byte, domain.Mode) (domain.NativeResult, error) {
	return b.call()
}
func (b *backend) Remove(context.Context, string) (domain.NativeResult, error) { return b.call() }
func (b *backend) Override(context.Context, string, string) (domain.NativeResult, error) {
	return b.call()
}
func (b *backend) Probe(context.Context, domain.ProbeRequest) (domain.Context, domain.NativeResult, error) {
	n, e := b.call()
	return domain.Context{OutboundTag: "selected"}, n, e
}

type prepareFunc func(context.Context, string, []byte) ([]byte, error)

func (f prepareFunc) Prepare(ctx context.Context, id string, data []byte) ([]byte, error) {
	return f(ctx, id, data)
}
func setup(b *backend, p services.Preparer) (*services.Service, *state) {
	state := &state{}
	state.generation.Store(1)
	s, err := services.New([]services.Binding{{ID: "one", Backend: b}, {ID: "disabled"}}, state, p)
	if err != nil {
		panic(err)
	}
	return s, state
}
