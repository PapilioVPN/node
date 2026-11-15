package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/modules/instances/app"
	"github.com/PapilioVPN/node/internal/modules/instances/domain"
)

func TestControlSelectsExistingRuntimeAndPreservesPartialOutcome(t *testing.T) {
	for name, operation := range map[string]func(*app.LifecycleService, context.Context, domain.InstanceID) (domain.Status, error){
		"start": (*app.LifecycleService).StartInstance,
		"stop":  (*app.LifecycleService).StopInstance,
	} {
		t.Run(name, func(t *testing.T) {
			operationErr := errors.New("controlled operation failure")
			executionErr := errors.New("previous execution failure")
			selected := &controlledRuntime{state: domain.Stopping, err: operationErr, failure: executionErr}
			other := &controlledRuntime{state: domain.Running}
			bindings := map[domain.InstanceID]*app.Lifecycle{
				"edge-a": app.NewLifecycle(domain.Instance{ID: "edge-a", Kind: domain.Xray}, selected),
				"edge-b": app.NewLifecycle(domain.Instance{ID: "edge-b", Kind: domain.Xray}, other),
			}
			service := app.NewLifecycleService(bindings)
			bindings["edge-a"] = bindings["edge-b"]
			value, err := operation(service, t.Context(), "edge-a")
			if !errors.Is(err, operationErr) || value.Instance.ID != "edge-a" || value.Instance.Kind != domain.Xray || value.State != domain.Stopping || value.Failure != executionErr {
				t.Fatalf("partial outcome or distinct causes lost: %+v, %v", value, err)
			}
			if selected.context != t.Context() || selected.calls != 1 || selected.observations != 1 || other.calls != 0 || other.observations != 0 {
				t.Fatal("control did not use exactly the requested bound runtime and context")
			}
			if name == "start" && (selected.starts != 1 || selected.stops != 0) || name == "stop" && (selected.starts != 0 || selected.stops != 1) {
				t.Fatal("control invoked the wrong lifecycle operation")
			}
		})
	}
}

func TestControlMissingOrCanceledRequestDoesNotTouchRuntime(t *testing.T) {
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	expired, cancelDeadline := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	for name, operation := range map[string]func(*app.LifecycleService, context.Context, domain.InstanceID) (domain.Status, error){
		"start": (*app.LifecycleService).StartInstance,
		"stop":  (*app.LifecycleService).StopInstance,
	} {
		t.Run(name, func(t *testing.T) {
			runtime := &controlledRuntime{state: domain.Running}
			service := app.NewLifecycleService(map[domain.InstanceID]*app.Lifecycle{
				"edge": app.NewLifecycle(domain.Instance{ID: "edge", Kind: domain.Xray}, runtime),
			})
			for _, test := range []struct {
				ctx   context.Context
				id    domain.InstanceID
				cause error
			}{
				{t.Context(), "unknown", domain.ErrNotFound},
				{t.Context(), "", domain.ErrNotFound},
				{t.Context(), " edge", domain.ErrNotFound},
				{canceled, "edge", context.Canceled},
				{canceled, "unknown", context.Canceled},
				{expired, "edge", context.DeadlineExceeded},
			} {
				value, err := operation(service, test.ctx, test.id)
				if !errors.Is(err, test.cause) || value.Instance.ID != "" {
					t.Fatalf("unexpected rejection for %q: %+v, %v", test.id, value, err)
				}
			}
			if runtime.calls != 0 || runtime.observations != 0 {
				t.Fatal("rejected request touched the native runtime")
			}
		})
	}
}
