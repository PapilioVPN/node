// Package services coordinates bounded native logger maintenance per execution.
package services

import (
	"context"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/domain"
	instancevalidators "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type Restarter interface {
	Reopen(context.Context) (domain.NativeResult, error)
}
type StatusReader interface {
	Status(context.Context, string) (instances.Status, error)
}
type Binding struct {
	ID        string
	Restarter Restarter
}
type entry struct {
	restarter           Restarter
	mu                  sync.Mutex
	active              bool
	uncertainGeneration uint64
	last                *domain.Operation
}
type Service struct {
	entries map[string]*entry
	status  StatusReader
	slots   chan struct{}
}

func New(bindings []Binding, status StatusReader) (*Service, error) {
	if status == nil {
		return nil, domain.ErrInvalidArgument
	}
	service := &Service{entries: make(map[string]*entry, len(bindings)), status: status, slots: make(chan struct{}, 8)}
	for _, binding := range bindings {
		if instancevalidators.ID(binding.ID) != nil || service.entries[binding.ID] != nil {
			return nil, domain.ErrInvalidArgument
		}
		service.entries[binding.ID] = &entry{restarter: binding.Restarter}
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
	result := domain.ControlStatus{InstanceID: id, Generation: state.Execution.Generation, ExecutionState: state.Execution.State, APIConfigured: selected.restarter != nil, RequestInFlight: selected.active, RequiresInstanceRestart: selected.uncertainGeneration != 0 && selected.uncertainGeneration == state.Execution.Generation, ObservedAt: time.Now().UTC()}
	if selected.last != nil {
		copy := *selected.last
		result.LastOperation = &copy
	}
	return result, nil
}
