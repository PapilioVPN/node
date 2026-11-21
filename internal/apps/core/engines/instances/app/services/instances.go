// Package services coordinates configured instance selection and execution ownership.
package services

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/PapilioVPN/node/internal/apps/core/engines/instances/app/validators"
	"github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
)

// Runtime owns native execution and retains evidence after exit. Start transfers
// a successful execution to Runtime; it must not inherit the request lifetime.
type Runtime interface {
	Start(context.Context) error
	Stop(context.Context) error
	Observe() domain.Execution
	Check(context.Context) (bool, error)
	APIConfigured() bool
	Close(context.Context) error
}

type Binding struct {
	ID      string
	Engine  string
	Runtime Runtime
}
type instance struct {
	binding  Binding
	mutation chan struct{}
}
type Service struct {
	entries  map[string]*instance
	ordered  []*instance
	closed   atomic.Bool
	lifetime context.Context
	cancel   context.CancelFunc
}

func New(bindings []Binding) (*Service, error) {
	lifetime, cancel := context.WithCancel(context.Background())
	s := &Service{entries: make(map[string]*instance, len(bindings)), lifetime: lifetime, cancel: cancel}
	for _, binding := range bindings {
		if validators.ID(binding.ID) != nil || binding.Engine == "" || binding.Runtime == nil || s.entries[binding.ID] != nil {
			cancel()
			return nil, domain.ErrInvalidArgument
		}
		entry := &instance{binding: binding, mutation: make(chan struct{}, 1)}
		s.entries[binding.ID] = entry
		s.ordered = append(s.ordered, entry)
	}
	return s, nil
}

func (s *Service) selectInstance(ctx context.Context, id string) (*instance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validators.ID(id); err != nil {
		return nil, err
	}
	entry := s.entries[id]
	if entry == nil {
		return nil, domain.ErrNotFound
	}
	return entry, nil
}
func observe(entry *instance) domain.Status {
	return domain.Status{ID: entry.binding.ID, Engine: entry.binding.Engine, Execution: entry.binding.Runtime.Observe(), ObservedAt: time.Now().UTC()}
}
func (s *Service) Status(ctx context.Context, id string) (domain.Status, error) {
	entry, err := s.selectInstance(ctx, id)
	if err != nil {
		return domain.Status{}, err
	}
	return observe(entry), nil
}

func (s *Service) Start(ctx context.Context, id string) (domain.Control, error) {
	return s.control(ctx, id, false, false)
}
func (s *Service) Stop(ctx context.Context, id string) (domain.Control, error) {
	return s.control(ctx, id, true, false)
}
func (s *Service) Restart(ctx context.Context, id string) (domain.Control, error) {
	return s.control(ctx, id, true, true)
}

func (s *Service) control(ctx context.Context, id string, stop, restart bool) (domain.Control, error) {
	result := domain.Control{Effect: domain.NotApplied}
	entry, err := s.selectInstance(ctx, id)
	if err != nil {
		return result, err
	}
	initial := observe(entry)
	result.Status = &initial
	if s.closed.Load() {
		return result, domain.ErrClosed
	}
	select {
	case entry.mutation <- struct{}{}:
	default:
		return result, domain.ErrBusy
	}
	defer func() { <-entry.mutation }()
	if s.closed.Load() {
		return result, domain.ErrClosed
	}
	operation, cancel := context.WithCancel(ctx)
	defer cancel()
	release := context.AfterFunc(s.lifetime, cancel)
	defer release()
	if err := operation.Err(); err != nil {
		return result, err
	}
	stopped := false
	if stop {
		err = entry.binding.Runtime.Stop(operation)
		stopped = err == nil
	} else {
		err = entry.binding.Runtime.Start(operation)
	}
	if err == nil && restart {
		if err = operation.Err(); err == nil {
			err = entry.binding.Runtime.Start(operation)
		}
	}
	current := observe(entry)
	result.Status = &current
	switch {
	case err == nil:
		result.Effect = domain.Completed
	case stopped && restart:
		result.Effect = domain.Partial
	case errors.Is(err, domain.ErrNotApplied):
		result.Effect = domain.NotApplied
	default:
		result.Effect = domain.Unknown
	}
	return result, err
}

func (s *Service) Check(ctx context.Context, id string) (domain.Readiness, error) {
	entry, err := s.selectInstance(ctx, id)
	if err != nil {
		return domain.Readiness{}, err
	}
	result := domain.Readiness{Status: observe(entry), APIState: domain.ProbeNotConfigured, TrafficState: domain.ProbeNotChecked}
	if entry.binding.Runtime.APIConfigured() {
		result.APIState = domain.ProbeNotChecked
	}
	if result.APIState == domain.ProbeNotConfigured || result.Status.Execution.State != domain.Running {
		result.CheckedAt = time.Now().UTC()
		return result, nil
	}
	ready, err := entry.binding.Runtime.Check(ctx)
	current := observe(entry)
	if current.Execution.Generation != result.Status.Execution.Generation || current.Execution.State != domain.Running {
		return domain.Readiness{}, errors.Join(domain.ErrExecutionChanged, err)
	}
	result.Status = current
	result.APIState = domain.ProbeUnavailable
	if ready && err == nil {
		result.APIState = domain.ProbeReady
	}
	result.CheckedAt = time.Now().UTC()
	// A failed native probe is an operation failure, never a successful healthy zero.
	return result, err
}

// StartAll preserves configured order. Its caller owns rollback even if Fx does
// not call OnStop after a failed OnStart.
func (s *Service) StartAll(ctx context.Context) error {
	for _, entry := range s.ordered {
		if _, err := s.Start(ctx, entry.binding.ID); err != nil {
			return err
		}
	}
	return nil
}

// Close seals and cancels admitted operations, waits for them, then closes native
// resources in reverse configuration order. Errors never assert completed cleanup.
func (s *Service) Close(ctx context.Context) error {
	s.closed.Store(true)
	s.cancel()
	var result error
	for i := len(s.ordered) - 1; i >= 0; i-- {
		entry := s.ordered[i]
		select {
		case entry.mutation <- struct{}{}:
		case <-ctx.Done():
			return errors.Join(result, ctx.Err())
		}
		err := entry.binding.Runtime.Close(ctx)
		<-entry.mutation
		result = errors.Join(result, err)
	}
	return result
}
