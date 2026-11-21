package services

import (
	"context"
	"errors"
	"sync"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	instancedomain "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type Repository interface {
	Metadata(context.Context) (domain.DesiredMetadata, error)
	Read(context.Context) (domain.Desired, error)
	Write(context.Context, uint64, []domain.Source) (domain.Persistence, error)
	Confirm(context.Context, uint64) (domain.DesiredMetadata, error)
}
type SourceChecker interface {
	CheckSources(context.Context, string, []domain.Source) (domain.Validation, error)
}
type Restarter interface {
	Restart(context.Context, string) (instancedomain.Control, error)
}
type StateBinding struct {
	ID         string
	Repository Repository
}
type stateEntry struct {
	repository Repository
	gate       chan struct{}
}

// StateService owns configuration changes, not process or filesystem resources.
// Two whole mutations bound retained source data; each instance has one writer.
type StateService struct {
	entries   map[string]*stateEntry
	checker   SourceChecker
	restarter Restarter
	mu        sync.Mutex
	closed    bool
	active    int
	capacity  chan struct{}
	lifetime  context.Context
	cancel    context.CancelFunc
	done      chan struct{}
}

func NewState(bindings []StateBinding, checker SourceChecker, restarter Restarter) (*StateService, error) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &StateService{entries: make(map[string]*stateEntry, len(bindings)), checker: checker, restarter: restarter, capacity: make(chan struct{}, 2), lifetime: ctx, cancel: cancel, done: make(chan struct{})}
	for _, binding := range bindings {
		if binding.ID == "" || s.entries[binding.ID] != nil {
			cancel()
			return nil, domain.ErrInvalidArgument
		}
		s.entries[binding.ID] = &stateEntry{repository: binding.Repository, gate: make(chan struct{}, 1)}
	}
	return s, nil
}

func (s *StateService) selectEntry(ctx context.Context, id string) (*stateEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, domain.ErrInvalidArgument
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, domain.ErrClosed
	}
	entry := s.entries[id]
	if entry == nil {
		return nil, domain.ErrNotFound
	}
	if entry.repository == nil {
		return nil, domain.ErrUnsupported
	}
	return entry, nil
}

func (s *StateService) Metadata(ctx context.Context, id string) (domain.DesiredMetadata, error) {
	entry, err := s.selectEntry(ctx, id)
	if err != nil {
		return domain.DesiredMetadata{}, err
	}
	return entry.repository.Metadata(ctx)
}
func (s *StateService) Read(ctx context.Context, id string) (domain.Desired, error) {
	entry, err := s.selectEntry(ctx, id)
	if err != nil {
		return domain.Desired{}, err
	}
	return entry.repository.Read(ctx)
}

func (s *StateService) begin(ctx context.Context, id string) (context.Context, *stateEntry, func(), error) {
	entry, err := s.selectEntry(ctx, id)
	if err != nil {
		return nil, nil, nil, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, nil, nil, domain.ErrClosed
	}
	select {
	case s.capacity <- struct{}{}:
	default:
		s.mu.Unlock()
		return nil, nil, nil, domain.ErrCapacity
	}
	select {
	case entry.gate <- struct{}{}:
	default:
		<-s.capacity
		s.mu.Unlock()
		return nil, nil, nil, domain.ErrCapacity
	}
	s.active++
	s.mu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	notified := make(chan struct{})
	stop := context.AfterFunc(s.lifetime, func() { cancel(); close(notified) })
	return ctx, entry, func() {
		if !stop() {
			<-notified
		}
		cancel()
		s.mu.Lock()
		defer s.mu.Unlock()
		<-entry.gate
		<-s.capacity
		s.active--
		if s.closed && s.active == 0 {
			close(s.done)
		}
	}, nil
}

func (s *StateService) Close(ctx context.Context) error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.cancel()
		if s.active == 0 {
			close(s.done)
		}
	}
	s.mu.Unlock()
	select {
	case <-s.done:
		return nil
	default:
	}
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func executionError(err error) error {
	switch {
	case errors.Is(err, instancedomain.ErrBusy):
		return errors.Join(domain.ErrCapacity, err)
	case errors.Is(err, instancedomain.ErrClosed), errors.Is(err, instancedomain.ErrUnsupported):
		return errors.Join(domain.ErrClosed, err)
	case errors.Is(err, instancedomain.ErrNotFound):
		return errors.Join(domain.ErrNotFound, err)
	default:
		return err
	}
}
