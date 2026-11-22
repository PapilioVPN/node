// Package services coordinates live outbound observations and mutations.
package services

import (
	"context"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
	instancevalidators "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type Client interface {
	Records(context.Context) ([]domain.Record, error)
	Add(context.Context, []byte) (domain.NativeResult, error)
	Remove(context.Context, string) (domain.NativeResult, error)
}
type StatusReader interface {
	Status(context.Context, string) (instances.Status, error)
}
type Binding struct {
	ID     string
	Client Client
}
type entry struct {
	client              Client
	mu                  sync.Mutex
	readers             int
	mutating            bool
	uncertainGeneration uint64
	last                *domain.Operation
}
type Service struct {
	entries          map[string]*entry
	status           StatusReader
	reads, mutations chan struct{}
}

func New(bindings []Binding, status StatusReader) (*Service, error) {
	if status == nil {
		return nil, domain.ErrInvalidArgument
	}
	service := &Service{entries: make(map[string]*entry, len(bindings)), status: status, reads: make(chan struct{}, 32), mutations: make(chan struct{}, 8)}
	for _, binding := range bindings {
		if instancevalidators.ID(binding.ID) != nil || service.entries[binding.ID] != nil {
			return nil, domain.ErrInvalidArgument
		}
		service.entries[binding.ID] = &entry{client: binding.Client}
	}
	return service, nil
}
func (s *Service) selectEntry(ctx context.Context, id string) (*entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if instancevalidators.ID(id) != nil {
		return nil, domain.ErrInvalidArgument
	}
	selected := s.entries[id]
	if selected == nil {
		return nil, domain.ErrNotFound
	}
	return selected, nil
}
func (s *Service) Status(ctx context.Context, id string) (domain.ControlStatus, error) {
	selected, err := s.selectEntry(ctx, id)
	if err != nil {
		return domain.ControlStatus{}, err
	}
	state, err := s.status.Status(ctx, id)
	if err != nil {
		return domain.ControlStatus{}, err
	}
	selected.mu.Lock()
	defer selected.mu.Unlock()
	result := domain.ControlStatus{InstanceID: id, Generation: state.Execution.Generation, ExecutionState: string(state.Execution.State), APIConfigured: selected.client != nil, RequestInFlight: selected.mutating, RequiresInstanceRestart: selected.uncertainGeneration != 0 && selected.uncertainGeneration == state.Execution.Generation, ObservedAt: time.Now().UTC()}
	if selected.last != nil {
		copy := *selected.last
		result.LastOperation = &copy
	}
	return result, nil
}
