package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/PapilioVPN/node/internal/modules/instances/app"
	"github.com/PapilioVPN/node/internal/modules/instances/domain"
)

func TestStatusSelectsExistingRuntimeAndPreservesFailure(t *testing.T) {
	cause := errors.New("completed execution failed")
	first := &controlledRuntime{state: domain.Exited, failure: cause}
	second := &controlledRuntime{state: domain.Running}
	bindings := map[domain.InstanceID]*app.Lifecycle{
		"edge-a": app.NewLifecycle(domain.Instance{ID: "edge-a", Kind: domain.Xray}, first),
		"edge-b": app.NewLifecycle(domain.Instance{ID: "edge-b", Kind: domain.Xray}, second),
	}
	service := app.NewStatusService(bindings)
	delete(bindings, "edge-a") // The process-owned index must not borrow a mutable map.
	status, err := service.GetInstanceStatus(t.Context(), "edge-a")
	if err != nil || status.Instance.ID != "edge-a" || status.State != domain.Exited || status.Failure != cause {
		t.Fatalf("instance outcome lost: %+v, %v", status, err)
	}
	if first.observations != 1 || second.observations != 0 || first.calls != 0 || second.calls != 0 {
		t.Fatal("status must observe only the requested instance without mutation")
	}
	status, err = service.GetInstanceStatus(t.Context(), "edge-b")
	if err != nil || status.Instance.ID != "edge-b" || status.State != domain.Running || status.Failure != nil {
		t.Fatalf("other instance affected: %+v, %v", status, err)
	}
}

func TestStatusMissingInstanceAndCancellationDoNotObserve(t *testing.T) {
	runtime := &controlledRuntime{state: domain.Running}
	service := app.NewStatusService(map[domain.InstanceID]*app.Lifecycle{
		"edge": app.NewLifecycle(domain.Instance{ID: "edge", Kind: domain.Xray}, runtime),
	})
	if _, err := service.GetInstanceStatus(t.Context(), "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing identity not distinguished: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.GetInstanceStatus(ctx, "edge"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if runtime.observations != 0 || runtime.calls != 0 {
		t.Fatal("missing/canceled read touched a runtime")
	}
}
