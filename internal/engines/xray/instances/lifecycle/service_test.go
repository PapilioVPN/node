package lifecycle

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestStatusDuringStartAndConflictingMutations(t *testing.T) {
	process := newTestChild(t)
	probing, ready := make(chan struct{}), make(chan struct{})
	service := newTestService(t, process, func(ctx context.Context, _ string) error {
		close(probing)
		select {
		case <-ready:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	result := make(chan error, 1)
	go func() {
		_, err := service.Start(t.Context(), "primary")
		result <- err
	}()
	<-probing
	expectState(t, service, Starting)
	if _, err := service.Start(t.Context(), "primary"); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent Start = %v", err)
	}
	if _, err := service.Stop(t.Context(), "primary"); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent Stop = %v", err)
	}
	close(ready)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	expectState(t, service, Ready)
	if _, err := service.Start(t.Context(), "primary"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Stop(t.Context(), "primary"); err != nil {
		t.Fatal(err)
	}
	expectState(t, service, Stopped)
}

func TestCanceledStartReapsChild(t *testing.T) {
	process := newTestChild(t)
	probing := make(chan struct{})
	service := newTestService(t, process, func(ctx context.Context, _ string) error {
		close(probing)
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := service.Start(ctx, "primary")
		result <- err
	}()
	<-probing
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Start error = %v, want canceled", err)
	}
	expectState(t, service, Failed)
	select {
	case <-process.exited:
	default:
		t.Fatal("child still running after canceled start")
	}
}

func TestUnexpectedExitIsObserved(t *testing.T) {
	process := newTestChild(t)
	service := newTestService(t, process, func(context.Context, string) error { return nil })
	if _, err := service.Start(t.Context(), "primary"); err != nil {
		t.Fatal(err)
	}
	service.mu.RLock()
	joined := service.run.done
	service.mu.RUnlock()
	process.exit()
	<-joined
	expectState(t, service, Failed)
}

func TestCloseCancelsStartAndRejectsFurtherMutations(t *testing.T) {
	process := newTestChild(t)
	probing := make(chan struct{})
	service := newTestService(t, process, func(ctx context.Context, _ string) error {
		close(probing)
		<-ctx.Done()
		return ctx.Err()
	})
	started := make(chan error, 1)
	go func() {
		_, err := service.Start(t.Context(), "primary")
		started <- err
	}()
	<-probing
	if err := service.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-started; !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Start error = %v, want unavailable", err)
	}
	expectState(t, service, Stopped)
	if _, err := service.Start(t.Context(), "primary"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Start after Close = %v, want unavailable", err)
	}
}

func newTestService(t *testing.T, process child, probe func(context.Context, string) error) *Service {
	t.Helper()
	service := New(Config{ID: "primary"})
	service.launch = func(Config) (child, error) { return process, nil }
	service.probe = probe
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return service
}

func expectState(t *testing.T, service *Service, state State) {
	t.Helper()
	got, err := service.GetStatus("primary")
	if err != nil || got.State != state {
		t.Fatalf("GetStatus = (%+v, %v), want %s", got, err, state)
	}
}

type testChild struct {
	once   sync.Once
	exited chan struct{}
}

func newTestChild(t *testing.T) *testChild {
	t.Helper()
	p := &testChild{exited: make(chan struct{})}
	t.Cleanup(p.exit)
	return p
}

func (p *testChild) socket() string { return "test.sock" }
func (p *testChild) exit()          { p.once.Do(func() { close(p.exited) }) }
func (p *testChild) wait() error    { <-p.exited; return nil }
func (p *testChild) terminate() error {
	p.exit()
	return nil
}
func (p *testChild) kill() error { return p.terminate() }
