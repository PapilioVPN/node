package services

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/domain"
)

var oldID = strings.Repeat("a", 64)
var newID = strings.Repeat("b", 64)
var executionID = strings.Repeat("c", 32)

type repository struct {
	state       domain.State
	calls       []string
	begin       func()
	finishError error
}

func (r *repository) State(context.Context) (domain.State, error) { return r.state, nil }
func (r *repository) Inspect(context.Context) (domain.Inventory, error) {
	return domain.Inventory{State: r.state}, nil
}
func (r *repository) Stage(context.Context, domain.Candidate) (domain.Release, error) {
	return domain.Release{}, nil
}
func (r *repository) Release(context.Context, string) (domain.Release, error) {
	return domain.Release{}, nil
}
func (r *repository) Begin(_ context.Context, revision uint64, target string) (domain.State, error) {
	r.calls = append(r.calls, "intent")
	r.state.Revision++
	r.state.Pending = target
	if r.begin != nil {
		r.begin()
	}
	return r.state, nil
}
func (r *repository) Select(_ context.Context, id string) error {
	r.calls = append(r.calls, "select:"+id)
	return nil
}
func (r *repository) Finish(_ context.Context, _ uint64, success bool) (domain.State, error) {
	r.calls = append(r.calls, "finish")
	if r.finishError != nil {
		return r.state, r.finishError
	}
	if success {
		r.state.Previous = r.state.Active
		r.state.Active = r.state.Pending
	}
	r.state.Pending = ""
	return r.state, nil
}

type runtime struct {
	repo         *repository
	checks       int
	unreadyFirst bool
	startError   error
	stopError    error
	cancel       context.CancelFunc
}

func (r *runtime) Start(ctx context.Context) error {
	r.repo.calls = append(r.repo.calls, "start")
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
		return ctx.Err()
	}
	err := r.startError
	r.startError = nil
	return err
}
func (r *runtime) Stop(ctx context.Context) error {
	r.repo.calls = append(r.repo.calls, "stop")
	if ctx.Err() != nil {
		return ctx.Err()
	}
	err := r.stopError
	r.stopError = nil
	return err
}
func (r *runtime) Check(context.Context) (bool, error) {
	r.checks++
	if r.unreadyFirst && r.checks == 1 {
		return false, domain.ErrRejected
	}
	return true, nil
}

type lifecycle struct {
	session  Session
	released bool
	err      error
}

func (l *lifecycle) Reserve(context.Context, string, string) (Session, func(), error) {
	if l.err != nil {
		return nil, nil, l.err
	}
	return l.session, func() { l.released = true }, nil
}
func fixture(t *testing.T) (*Service, *repository, *runtime, *lifecycle) {
	t.Helper()
	r := &repository{state: domain.State{InstanceID: "a", Revision: 1, Active: oldID}}
	run := &runtime{repo: r}
	l := &lifecycle{session: run}
	s, err := New([]Binding{{ID: "a", Repository: r}}, l)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s, r, run, l
}
func request() domain.Activation {
	return domain.Activation{ExpectedRevision: 1, ExecutionID: executionID, ReleaseID: newID}
}
func TestActivationUsesDurableIntentAndExistingLifecycle(t *testing.T) {
	s, r, run, l := fixture(t)
	receipt, err := s.Activate(context.Background(), "a", request())
	if err != nil || receipt.Outcome != "completed" || !receipt.Ready || receipt.State.Active != newID || receipt.State.Previous != oldID || receipt.State.Revision != 2 || !l.released || run.checks != 2 {
		t.Fatalf("receipt: %+v %v", receipt, err)
	}
	want := []string{"intent", "stop", "select:" + newID, "start", "finish"}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatal(r.calls)
	}
}
func TestFailureAndCancellationRestoreBeforeRelease(t *testing.T) {
	for _, phase := range []string{"start", "stop", "cancel_after_intent", "cancel_after_start"} {
		t.Run(phase, func(t *testing.T) {
			s, r, run, l := fixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch phase {
			case "start":
				run.startError = domain.ErrRejected
			case "stop":
				run.stopError = domain.ErrUnavailable
			case "cancel_after_intent":
				r.begin = cancel
			case "cancel_after_start":
				run.cancel = cancel
			}
			got, err := s.Activate(ctx, "a", request())
			if err == nil || got.Outcome != "restored" || !got.Ready || got.State.Pending != "" || got.State.Active != oldID || !l.released {
				t.Fatalf("restore: %+v %v", got, err)
			}
			tail := r.calls[len(r.calls)-4:]
			if !reflect.DeepEqual(tail, []string{"stop", "select:" + oldID, "start", "finish"}) {
				t.Fatal(r.calls)
			}
		})
	}
}
func TestUncertainCommitRetainsIntentWithoutBlindRollback(t *testing.T) {
	s, r, _, _ := fixture(t)
	r.finishError = domain.ErrUnavailable
	got, err := s.Activate(context.Background(), "a", request())
	if !errors.Is(err, domain.ErrUnavailable) || got.Outcome != "recovery_required" || got.State.Pending != newID {
		t.Fatalf("commit %+v %v", got, err)
	}
	if len(r.calls) != 5 {
		t.Fatal("blind recovery", r.calls)
	}
	r.finishError = nil
	got, err = s.Recover(context.Background(), "a", domain.Activation{ExpectedRevision: 2, ExecutionID: executionID})
	if err != nil || got.Outcome != "restored" || got.State.Active != oldID {
		t.Fatal(got, err)
	}
}
func TestStaleAndUnresolvedSelectionsDoNotStop(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{true: "pending", false: "stale"}[pending], func(t *testing.T) {
			s, r, _, _ := fixture(t)
			q := request()
			if pending {
				r.state.Pending = newID
			} else {
				q.ExpectedRevision = 0
			}
			got, err := s.Activate(context.Background(), "a", q)
			if err == nil || got.Outcome != "not_applied" || len(r.calls) != 0 {
				t.Fatal(got, err, r.calls)
			}
		})
	}
}
func TestAdmissionAndCloseJoinRecovery(t *testing.T) {
	s, r, _, _ := fixture(t)
	entered := make(chan struct{})
	resume := make(chan struct{})
	r.begin = func() { close(entered); <-resume }
	done := make(chan struct{})
	go func() { defer close(done); _, _ = s.Activate(context.Background(), "a", request()) }()
	<-entered
	if _, err := s.Inspect(context.Background(), "a"); !errors.Is(err, domain.ErrBusy) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := s.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	close(resume)
	<-done
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.state.Pending != "" || r.state.Active != oldID {
		t.Fatal(r.state)
	}
}
func TestActivationRequiresReservation(t *testing.T) {
	s, r, _, l := fixture(t)
	l.err = domain.ErrUnsupported
	_, err := s.Activate(context.Background(), "a", request())
	if !errors.Is(err, domain.ErrUnsupported) || len(r.calls) != 0 {
		t.Fatal(err)
	}
}

func TestRollbackCanRecoverAnUnreadyCommittedRelease(t *testing.T) {
	s, r, run, _ := fixture(t)
	r.state.Previous = newID
	run.unreadyFirst = true
	got, err := s.Rollback(context.Background(), "a", request())
	if err != nil || got.Outcome != "completed" || got.State.Active != newID || !got.Ready {
		t.Fatal(got, err)
	}
}
