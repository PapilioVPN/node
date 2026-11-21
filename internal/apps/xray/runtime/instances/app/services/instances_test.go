package services

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type runtimeFixture struct {
	mu           sync.Mutex
	execution    domain.Execution
	failStart    bool
	startEntered chan struct{}
	release      chan struct{}
	events       *[]string
	id           string
}

func (r *runtimeFixture) Start(ctx context.Context) error {
	if r.startEntered != nil {
		close(r.startEntered)
		select {
		case <-r.release:
		case <-ctx.Done():
			return errors.Join(domain.ErrNotApplied, ctx.Err())
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failStart {
		return errors.Join(domain.ErrNotApplied, errors.New("private start failure"))
	}
	r.execution.State = domain.Running
	r.execution.Generation++
	if r.events != nil {
		*r.events = append(*r.events, "start:"+r.id)
	}
	return nil
}
func (r *runtimeFixture) Stop(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.execution.State = domain.Stopped
	return ctx.Err()
}
func (r *runtimeFixture) Observe() domain.Execution {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.execution
}
func (r *runtimeFixture) Check(context.Context) (bool, error) { return true, nil }
func (r *runtimeFixture) APIConfigured() bool                 { return true }
func (r *runtimeFixture) Close(ctx context.Context) error {
	if r.events != nil {
		*r.events = append(*r.events, "close:"+r.id)
	}
	return r.Stop(ctx)
}

func TestRestartFailureRetainsStoppedExecutionAndPartialEffect(t *testing.T) {
	r := &runtimeFixture{execution: domain.Execution{State: domain.Running, Generation: 7}, failStart: true}
	s, err := New([]Binding{{ID: "a", Engine: "xray", Runtime: r}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Restart(context.Background(), "a")
	if err == nil || result.Effect != domain.Partial || result.Status.Execution.State != domain.Stopped || result.Status.Execution.Generation != 7 {
		t.Fatalf("restart: %+v %v", result, err)
	}
}
func TestCompetingMutationRejectedWhileStatusRemainsReadable(t *testing.T) {
	r := &runtimeFixture{startEntered: make(chan struct{}), release: make(chan struct{})}
	s, _ := New([]Binding{{ID: "a", Engine: "xray", Runtime: r}})
	done := make(chan error, 1)
	go func() { _, err := s.Start(context.Background(), "a"); done <- err }()
	<-r.startEntered
	result, err := s.Stop(context.Background(), "a")
	if !errors.Is(err, domain.ErrBusy) || result.Effect != domain.NotApplied {
		t.Fatalf("competing stop: %+v %v", result, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := s.Status(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	close(r.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestLifecycleOrderAndClosedAdmission(t *testing.T) {
	var events []string
	s, _ := New([]Binding{{"a", "xray", &runtimeFixture{id: "a", events: &events}}, {"b", "xray", &runtimeFixture{id: "b", events: &events}}})
	if err := s.StartAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"start:a", "start:b", "close:b", "close:a"}) {
		t.Fatal(events)
	}
	if result, err := s.Start(context.Background(), "a"); !errors.Is(err, domain.ErrClosed) || result.Effect != domain.NotApplied {
		t.Fatal(result, err)
	}
}
func TestCloseCancelsAdmittedMutationAndJoins(t *testing.T) {
	r := &runtimeFixture{startEntered: make(chan struct{}), release: make(chan struct{})}
	s, _ := New([]Binding{{ID: "a", Engine: "xray", Runtime: r}})
	done := make(chan error, 1)
	go func() { _, err := s.Start(context.Background(), "a"); done <- err }()
	<-r.startEntered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(<-done, context.Canceled) {
		t.Fatal("admitted operation not canceled")
	}
}
func TestUnknownAndInvalidSelectionDoNotExecute(t *testing.T) {
	s, _ := New(nil)
	for _, test := range []struct {
		id    string
		cause error
	}{{"missing", domain.ErrNotFound}, {"../bad", domain.ErrInvalidArgument}} {
		result, err := s.Start(context.Background(), test.id)
		if !errors.Is(err, test.cause) || result.Effect != domain.NotApplied || result.Status != nil {
			t.Fatalf("%+v %v", result, err)
		}
	}
}

type changingProbe struct {
	runtimeFixture
	cancel context.CancelFunc
}

func (r *changingProbe) Check(ctx context.Context) (bool, error) {
	r.mu.Lock()
	r.execution.Generation++
	r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
	}
	return ctx.Err() == nil, ctx.Err()
}
func TestReadinessCannotDescribeADifferentExecution(t *testing.T) {
	r := &changingProbe{runtimeFixture: runtimeFixture{execution: domain.Execution{State: domain.Running, Generation: 4}}}
	s, _ := New([]Binding{{ID: "a", Engine: "xray", Runtime: r}})
	if _, err := s.Check(context.Background(), "a"); !errors.Is(err, domain.ErrExecutionChanged) {
		t.Fatalf("stale readiness accepted: %v", err)
	}
}

func TestReadinessGenerationChangePreservesCancellationCause(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &changingProbe{runtimeFixture: runtimeFixture{execution: domain.Execution{State: domain.Running, Generation: 4}}, cancel: cancel}
	s, _ := New([]Binding{{ID: "a", Engine: "xray", Runtime: r}})
	if _, err := s.Check(ctx, "a"); !errors.Is(err, context.Canceled) || !errors.Is(err, domain.ErrExecutionChanged) {
		t.Fatalf("lost cancellation: %v", err)
	}
}
