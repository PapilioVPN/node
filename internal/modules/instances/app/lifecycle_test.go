package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/PapilioVPN/node/internal/modules/instances/app"
	"github.com/PapilioVPN/node/internal/modules/instances/domain"
)

func TestLifecyclePreservesIdentityContextAndPartialOutcomes(t *testing.T) {
	cause := errors.New("runtime reported an interrupted operation")
	runtime := &controlledRuntime{state: domain.Running, err: cause}
	instance := domain.Instance{ID: "edge-a", Kind: domain.Xray}
	service := app.NewLifecycle(instance, runtime)
	for name, operation := range map[string]func(context.Context) (domain.Status, error){
		"start": service.Start, "stop": service.Stop,
	} {
		t.Run(name, func(t *testing.T) {
			status, err := operation(t.Context())
			if err != cause || status.Instance != instance || status.State != domain.Running {
				t.Fatalf("status = %+v, error = %v; partial outcome or cause was lost", status, err)
			}
			if runtime.context != t.Context() {
				t.Fatal("operation context was replaced")
			}
		})
	}
}

func TestLifecycleStatusDoesNotMutateRuntime(t *testing.T) {
	failure := errors.New("runtime exited unexpectedly")
	runtime := &controlledRuntime{state: domain.Exited, failure: failure}
	service := app.NewLifecycle(domain.Instance{ID: "edge-b", Kind: domain.Xray}, runtime)
	status, err := service.Status(t.Context())
	if err != nil || status.State != domain.Exited || status.Failure != failure || runtime.calls != 0 {
		t.Fatalf("status = %+v, error = %v, mutations = %d", status, err, runtime.calls)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.Status(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

type controlledRuntime struct {
	state   domain.State
	err     error
	failure error
	context context.Context
	calls   int
}

func (r *controlledRuntime) Start(ctx context.Context) error {
	r.context = ctx
	r.calls++
	return r.err
}

func (r *controlledRuntime) Stop(ctx context.Context) error {
	r.context = ctx
	r.calls++
	return r.err
}

func (r *controlledRuntime) Observe() (domain.State, error) { return r.state, r.failure }
