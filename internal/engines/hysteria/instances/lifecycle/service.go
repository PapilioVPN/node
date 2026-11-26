package lifecycle

import (
	"context"
	"crypto/sha256"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/engines/hysteria/internal/fault"
)

const startTimeout = 5 * time.Second

type Service struct {
	entries map[string]*entry
	ctx     context.Context
	cancel  context.CancelFunc
}

type entry struct {
	backend  Backend
	gate     chan struct{}
	mu       sync.RWMutex
	state    State
	sequence uint64
	current  Execution
	digest   [sha256.Size]byte
}

func New(backends map[string]Backend) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{entries: make(map[string]*entry, len(backends)), ctx: ctx, cancel: cancel}
	for id, backend := range backends {
		s.entries[id] = &entry{backend: backend, gate: make(chan struct{}, 1), state: Stopped}
	}
	return s
}

func (s *Service) lookup(ctx context.Context, id string) (*entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, fault.ErrInvalid
	}
	e := s.entries[id]
	if e == nil {
		return nil, fault.ErrNotFound
	}
	return e, nil
}

func (e *entry) observed() State {
	if e.state == Ready && ended(e.current.Run) {
		return Failed
	}
	return e.state
}

func ended(run Run) bool {
	select {
	case <-run.Done():
		return true
	default:
		return false
	}
}

func (e *entry) status(id string, action Action) Status {
	e.mu.RLock()
	defer e.mu.RUnlock()
	state := e.observed()
	status := Status{ID: id, State: state, Action: action}
	if state == Ready {
		digest := e.digest
		status.Version, status.Digest = e.current.Run.Version(), &digest
	}
	return status
}

func (s *Service) GetStatus(ctx context.Context, id string) (Status, error) {
	e, err := s.lookup(ctx, id)
	if err != nil {
		return Status{}, err
	}
	return e.status(id, 0), nil
}

func (s *Service) Current(ctx context.Context, id string) (Execution, error) {
	e, err := s.lookup(ctx, id)
	if err != nil {
		return Execution{}, err
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.observed() != Ready {
		return Execution{}, fault.ErrInactive
	}
	return e.current, nil
}

func (e *entry) admit() bool {
	select {
	case e.gate <- struct{}{}:
		return true
	default:
		return false
	}
}
func (e *entry) release() { <-e.gate }

func (s *Service) operation(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithTimeout(parent, startTimeout)
	stop := context.AfterFunc(s.ctx, cancel)
	if s.ctx.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }
}

func (s *Service) Start(ctx context.Context, req ConfigRequest) (Status, error) {
	e, err := s.lookup(ctx, req.ID)
	if err != nil {
		return Status{}, err
	}
	if !e.admit() {
		return e.status(req.ID, 0), fault.ErrBusy
	}
	defer e.release()
	ctx, cancel := s.operation(ctx)
	defer cancel()
	source, err := e.backend.Source(ctx, req.Source)
	if err != nil {
		return e.status(req.ID, 0), err
	}
	digest := sha256.Sum256(source)

	e.mu.RLock()
	state, same := e.observed(), e.digest == digest
	e.mu.RUnlock()
	if state == Ready && same {
		replay := e.status(req.ID, Heartbeat)
		if replay.State == Ready {
			return replay, ctx.Err()
		}
		state = replay.State
	}
	candidate, err := e.backend.Prepare(ctx, source)
	if err != nil {
		return e.status(req.ID, 0), err
	}
	if err := ctx.Err(); err != nil {
		return e.status(req.ID, 0), err
	}

	action := Started
	switch state {
	case Ready:
		action = Restarted
		e.stop(ctx)
	case Failed:
		action = Recovered
	}
	if err := ctx.Err(); err != nil {
		return e.status(req.ID, action), err
	}
	e.mu.Lock()
	e.sequence++
	sequence := e.sequence
	e.state, e.current = Starting, Execution{}
	e.mu.Unlock()

	run, err := candidate.Launch(ctx, sequence)
	e.mu.Lock()
	if err == nil {
		switch {
		case ctx.Err() != nil:
			err = ctx.Err()
		case ended(run):
			err = fault.ErrUnavailable
		default:
			e.state = Ready
			e.current = Execution{Number: sequence, ReadyAt: time.Now(), Run: run}
			e.digest = digest
		}
	}
	e.mu.Unlock()

	if err != nil {
		if run != nil {
			run.Stop(ctx)
		}
		e.mu.Lock()
		e.state = Failed
		if ctx.Err() != nil {
			e.state = Stopped
		}
		e.mu.Unlock()
	}
	result := e.status(req.ID, action)
	if err == nil && result.State != Ready {
		err = fault.ErrUnavailable
	}
	return result, err
}

func (e *entry) stop(ctx context.Context) {
	e.mu.Lock()
	run := e.current.Run
	if run != nil {
		e.state = Stopping
	}
	e.mu.Unlock()
	if run != nil {
		run.Stop(ctx)
	}
	e.mu.Lock()
	e.state, e.current, e.digest = Stopped, Execution{}, [sha256.Size]byte{}
	e.mu.Unlock()
}

func (s *Service) Stop(ctx context.Context, id string) (Status, error) {
	e, err := s.lookup(ctx, id)
	if err != nil {
		return Status{}, err
	}
	if !e.admit() {
		return e.status(id, 0), fault.ErrBusy
	}
	defer e.release()
	e.stop(ctx)
	return e.status(id, 0), ctx.Err()
}

func (s *Service) Close(ctx context.Context) error {
	s.cancel()
	// Cancellation aborts any in-flight start. Join every owned execution even
	// when the caller's graceful-stop deadline has elapsed.
	for _, e := range s.entries {
		e.gate <- struct{}{}
		e.stop(ctx)
		e.release()
	}
	return ctx.Err()
}
