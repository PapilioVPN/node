// Package services coordinates asset preparation and explicit recoverable activation.
package services

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/domain"
)

type Repository interface {
	Inspect(context.Context) (domain.Inventory, error)
	State(context.Context) (domain.State, error)
	Stage(context.Context, domain.Candidate) (domain.Release, error)
	Release(context.Context, string) (domain.Release, error)
	Begin(context.Context, uint64, string) (domain.State, error)
	Select(context.Context, string) error
	Finish(context.Context, uint64, bool) (domain.State, error)
}

// Session borrows an existing Instances runtime while its owner reserves all
// lifecycle mutations (including shutdown/lease expiry). Release must join all
// use before returning the reservation; implementations must reject stale IDs.
type Session interface {
	Start(context.Context) error
	Stop(context.Context) error
	Check(context.Context) (bool, error)
}
type Lifecycle interface {
	Reserve(context.Context, string, string) (Session, func(), error)
}
type Binding struct {
	ID         string
	Repository Repository
}
type entry struct {
	repository Repository
	gate       chan struct{}
}
type Service struct {
	entries   map[string]*entry
	lifecycle Lifecycle
	slots     chan struct{}
	mu        sync.Mutex
	closed    bool
	active    int
	done      chan struct{}
	lifetime  context.Context
	cancel    context.CancelFunc
}

func New(bindings []Binding, lifecycle Lifecycle) (*Service, error) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{entries: map[string]*entry{}, done: make(chan struct{}), lifecycle: lifecycle, slots: make(chan struct{}, 2), lifetime: ctx, cancel: cancel}
	for _, b := range bindings {
		if !validators.ID(b.ID) || b.Repository == nil || s.entries[b.ID] != nil {
			cancel()
			return nil, domain.ErrInvalidArgument
		}
		s.entries[b.ID] = &entry{repository: b.Repository, gate: make(chan struct{}, 1)}
	}
	return s, nil
}
func (s *Service) admit(parent context.Context, id string) (context.Context, Repository, func(), error) {
	if err := parent.Err(); err != nil {
		return nil, nil, nil, err
	}
	if !validators.ID(id) {
		return nil, nil, nil, domain.ErrInvalidArgument
	}
	repo := s.entries[id]
	if repo == nil {
		return nil, nil, nil, domain.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil, nil, domain.ErrUnavailable
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return nil, nil, nil, domain.ErrBusy
	}
	select {
	case repo.gate <- struct{}{}:
	default:
		<-s.slots
		return nil, nil, nil, domain.ErrBusy
	}
	s.active++
	ctx, cancel := context.WithCancel(parent)
	joined := make(chan struct{})
	stop := context.AfterFunc(s.lifetime, func() { cancel(); close(joined) })
	return ctx, repo.repository, func() {
		if !stop() {
			<-joined
		}
		cancel()
		s.mu.Lock()
		defer s.mu.Unlock()
		<-repo.gate
		<-s.slots
		s.active--
		if s.closed && s.active == 0 {
			close(s.done)
		}
	}, nil
}
func (s *Service) Inspect(ctx context.Context, id string) (domain.Inventory, error) {
	ctx, r, release, err := s.admit(ctx, id)
	if err != nil {
		return domain.Inventory{}, err
	}
	defer release()
	return r.Inspect(ctx)
}
func (s *Service) Stage(ctx context.Context, id string, c domain.Candidate) (domain.Release, error) {
	ctx, r, release, err := s.admit(ctx, id)
	if err != nil {
		return domain.Release{}, err
	}
	defer release()
	return r.Stage(ctx, c)
}

// Each selected repository has a separate nonwaiting operation gate across the
// whole transition, rather than a mutex over I/O. Repository implementations also
// serialize individual I/O. The gate is shared by Activate, Rollback and Recover.
// Per-instance transitions are reserved in Instances before durable intent.
func (s *Service) Activate(ctx context.Context, id string, q domain.Activation) (domain.Receipt, error) {
	return s.activate(ctx, id, q, false)
}
func (s *Service) Rollback(ctx context.Context, id string, q domain.Activation) (domain.Receipt, error) {
	return s.activate(ctx, id, q, true)
}
func (s *Service) activate(ctx context.Context, id string, q domain.Activation, previous bool) (domain.Receipt, error) {
	result := domain.Receipt{Outcome: "not_applied"}
	if len(q.ExecutionID) != 32 || !validators.Digest(q.ExecutionID+q.ExecutionID) || (!previous && !validators.Digest(q.ReleaseID)) {
		return result, domain.ErrInvalidArgument
	}
	ctx, r, release, err := s.admit(ctx, id)
	if err != nil {
		return result, err
	}
	defer release()
	if s.lifecycle == nil {
		return result, domain.ErrUnsupported
	}
	session, unlock, err := s.lifecycle.Reserve(ctx, id, q.ExecutionID)
	if err != nil {
		return result, err
	}
	defer unlock()
	inventory, err := r.Inspect(ctx)
	if err != nil {
		return result, err
	}
	result.State = inventory.State
	if inventory.State.Revision != q.ExpectedRevision {
		return result, domain.ErrConflict
	}
	if inventory.State.Pending != "" {
		return result, domain.ErrRecovery
	}
	if previous {
		q.ReleaseID = inventory.State.Previous
		if q.ReleaseID == "" {
			return result, domain.ErrNotFound
		}
	}
	if _, err = r.Release(ctx, q.ReleaseID); err != nil {
		return result, err
	}
	if !previous {
		ready, err := session.Check(ctx)
		if err != nil || !ready {
			return result, errors.Join(domain.ErrRejected, err)
		}
	}
	state, err := r.Begin(ctx, q.ExpectedRevision, q.ReleaseID)
	result.State = state
	if err != nil {
		if state.Pending != "" {
			result.Outcome = "recovery_required"
		}
		return result, err
	}
	result.Outcome = "recovery_required"
	if err = session.Stop(ctx); err == nil {
		err = r.Select(ctx, q.ReleaseID)
	}
	if err == nil {
		err = session.Start(ctx)
	}
	if err == nil {
		err = awaitReady(ctx, session)
	}
	if err != nil {
		// Once intent is durable, cancellation/shutdown still joins a bounded recovery
		// under the same reservation. No background replay or detached goroutine.
		recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		restored, recoveryErr := restore(recovery, r, session, state)
		if recoveryErr == nil {
			result.State = restored
			result.Outcome = "restored"
			result.Ready = true
		}
		result.CompletedAt = time.Now().UTC()
		return result, errors.Join(err, recoveryErr)
	}
	// A commit-sync error may already have published the ready replacement. Do not
	// blindly roll it back using a stale intent; explicit readback/recovery follows.
	result.State, err = r.Finish(ctx, state.Revision, true)
	if err == nil {
		result.Outcome = "completed"
		result.Ready = true
	}
	result.CompletedAt = time.Now().UTC()
	return result, err
}
func (s *Service) Recover(ctx context.Context, id string, q domain.Activation) (domain.Receipt, error) {
	result := domain.Receipt{Outcome: "not_applied"}
	if len(q.ExecutionID) != 32 || !validators.Digest(q.ExecutionID+q.ExecutionID) {
		return result, domain.ErrInvalidArgument
	}
	ctx, r, release, err := s.admit(ctx, id)
	if err != nil {
		return result, err
	}
	defer release()
	if s.lifecycle == nil {
		return result, domain.ErrUnsupported
	}
	session, unlock, err := s.lifecycle.Reserve(ctx, id, q.ExecutionID)
	if err != nil {
		return result, err
	}
	defer unlock()
	state, err := r.State(ctx)
	if err != nil {
		return result, err
	}
	result.State = state
	if state.Revision != q.ExpectedRevision {
		return result, domain.ErrConflict
	}
	if state.Pending == "" {
		return result, domain.ErrConflict
	}
	result.Outcome = "recovery_required"
	if _, err := r.Release(ctx, state.Active); err != nil {
		return result, err
	}
	recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result.State, err = restore(recovery, r, session, state)
	if err == nil {
		result.Ready = true
		result.Outcome = "restored"
	}
	result.CompletedAt = time.Now().UTC()
	return result, err
}
func restore(ctx context.Context, r Repository, session Session, state domain.State) (domain.State, error) {
	if err := session.Stop(ctx); err != nil {
		return state, err
	}
	if err := r.Select(ctx, state.Active); err != nil {
		return state, err
	}
	if err := session.Start(ctx); err != nil {
		return state, err
	}
	if err := awaitReady(ctx, session); err != nil {
		return state, err
	}
	return r.Finish(ctx, state.Revision, false)
}
func awaitReady(ctx context.Context, s Session) error {
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	timer := time.NewTicker(100 * time.Millisecond)
	defer timer.Stop()
	for i := 0; i < 30; i++ {
		probe, release := context.WithTimeout(bounded, 150*time.Millisecond)
		ready, err := s.Check(probe)
		release()
		if ready && err == nil {
			return nil
		}
		select {
		case <-bounded.Done():
			return errors.Join(domain.ErrRejected, bounded.Err())
		case <-timer.C:
		}
	}
	return domain.ErrRejected
}

// Close seals admission and cancels requests, then joins their bounded rollback.
// Callers must await Close before closing repositories or Instances resources.
func (s *Service) Close(ctx context.Context) error {
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
	case <-ctx.Done():
		return ctx.Err()
	}
}
