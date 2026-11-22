// Package services coordinates live inbound observations and mutations.
package services

import (
	"context"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	instancevalidators "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type Client interface {
	Records(context.Context) ([]domain.Record, error)
	Tags(context.Context) ([]string, error)
	Add(context.Context, []byte) (domain.NativeResult, error)
	Remove(context.Context, string) (domain.NativeResult, error)
}
type SummaryReader interface {
	Summaries(context.Context) ([]domain.Summary, error)
	Compatibility(context.Context, string) (domain.Summary, error)
}
type StatusReader interface {
	Status(context.Context, string) (instances.Status, error)
}
type Binding struct {
	Projection SummaryReader
	ID         string
	Client     Client
}
type entry struct {
	projection          SummaryReader
	client              Client
	mu                  sync.Mutex
	epoch               uint64
	readers             int
	mutating            bool
	uncertainGeneration uint64
	last                *domain.Operation
}
type Service struct {
	records          pageStore[domain.Record]
	tags             pageStore[string]
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
		service.entries[binding.ID] = &entry{client: binding.Client, projection: binding.Projection}
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
		copy := cloneOperation(*selected.last)
		result.LastOperation = &copy
	}
	return result, nil
}

func cloneOperation(operation domain.Operation) domain.Operation {
	if operation.Replacement != nil {
		copy := *operation.Replacement
		operation.Replacement = &copy
	}
	return operation
}
