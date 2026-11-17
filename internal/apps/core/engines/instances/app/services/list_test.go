package services_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/core/engines/instances/app/services"
	"github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
)

func TestStatusPagesObserveOnlySelectedInstances(t *testing.T) {
	for _, count := range []int{1, 10, 100} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			bindings := make(map[domain.InstanceID]*services.Lifecycle, count)
			runtimes := make([]*controlledRuntime, count)
			executionErr := errors.New("controlled completed execution failure")
			for i := count - 1; i >= 0; i-- {
				id := domain.InstanceID(fmt.Sprintf("edge-%03d", i))
				runtime := &controlledRuntime{state: domain.Running}
				if i == 0 {
					runtime.state, runtime.failure = domain.Exited, executionErr
				}
				runtimes[i] = runtime
				bindings[id] = services.NewLifecycle(domain.Instance{ID: id, Kind: domain.Xray}, runtime)
			}
			service := services.NewLifecycleService(bindings)
			clear(bindings)
			var after domain.InstanceID
			seen := 0
			for {
				page, err := service.ListInstanceStatuses(t.Context(), domain.StatusPageInput{AfterID: after, PageSize: 3})
				if err != nil {
					t.Fatal(err)
				}
				size := min(3, count-seen)
				if len(page.Items) != size {
					t.Fatalf("page length %d; want %d", len(page.Items), size)
				}
				for j, item := range page.Items {
					index := seen + j
					id := domain.InstanceID(fmt.Sprintf("edge-%03d", index))
					if item.Instance.ID != id || item.Instance.Kind != domain.Xray || item.State != runtimes[index].state || item.Failure != runtimes[index].failure {
						t.Fatalf("wrong selection/outcome: %+v", item)
					}
				}
				seen += size
				for i, runtime := range runtimes {
					observed := 0
					if i < seen {
						observed = 1
					}
					if runtime.observations != observed || runtime.calls != 0 {
						t.Fatalf("runtime %d: observed %d, mutated %d; want %d observations", i, runtime.observations, runtime.calls, observed)
					}
				}
				if seen == count {
					if page.NextAfterID != "" {
						t.Fatalf("invented continuation: %q", page.NextAfterID)
					}
					break
				}
				if page.NextAfterID != page.Items[len(page.Items)-1].Instance.ID {
					t.Fatal("cursor is not the last emitted identity")
				}
				after = page.NextAfterID
			}
		})
	}
}

func TestStatusPageCursorBoundsAndResultOwnership(t *testing.T) {
	runtime := &controlledRuntime{state: domain.Running}
	service := services.NewLifecycleService(map[domain.InstanceID]*services.Lifecycle{
		"A": services.NewLifecycle(domain.Instance{ID: "A"}, runtime),
		"b": services.NewLifecycle(domain.Instance{ID: "b"}, runtime),
		"c": services.NewLifecycle(domain.Instance{ID: "c"}, runtime),
	})
	page, err := service.ListInstanceStatuses(t.Context(), domain.StatusPageInput{AfterID: "az", PageSize: math.MaxInt})
	if err != nil || len(page.Items) != 2 || page.Items[0].Instance.ID != "b" || page.Items[1].Instance.ID != "c" || page.NextAfterID != "" || runtime.observations != 2 {
		t.Fatalf("cursor/size bounds: %+v, %v", page, err)
	}
	page.Items[0].Instance.ID = "caller.changed"
	again, err := service.ListInstanceStatuses(t.Context(), domain.StatusPageInput{AfterID: "A", PageSize: 1})
	if err != nil || len(again.Items) != 1 || again.Items[0].Instance.ID != "b" || again.NextAfterID != "b" {
		t.Fatalf("caller changed stored membership: %+v, %v", again, err)
	}
	for _, after := range []domain.InstanceID{"c", "zzz"} {
		empty, err := service.ListInstanceStatuses(t.Context(), domain.StatusPageInput{AfterID: after, PageSize: 1})
		if err != nil || len(empty.Items) != 0 || empty.NextAfterID != "" {
			t.Fatalf("end cursor: %+v, %v", empty, err)
		}
	}
	empty, err := services.NewLifecycleService(nil).ListInstanceStatuses(t.Context(), domain.StatusPageInput{PageSize: 1})
	if err != nil || len(empty.Items) != 0 || empty.NextAfterID != "" {
		t.Fatalf("empty configuration: %+v, %v", empty, err)
	}
}

func TestInvalidOrCanceledPageDoesNotObserve(t *testing.T) {
	runtime := &controlledRuntime{state: domain.Running}
	service := services.NewLifecycleService(map[domain.InstanceID]*services.Lifecycle{"edge": services.NewLifecycle(domain.Instance{ID: "edge"}, runtime)})
	for _, size := range []int{0, -1} {
		if _, err := service.ListInstanceStatuses(t.Context(), domain.StatusPageInput{PageSize: size}); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("invalid size: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.ListInstanceStatuses(ctx, domain.StatusPageInput{PageSize: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if runtime.observations != 0 || runtime.calls != 0 {
		t.Fatal("invalid/canceled selection touched a runtime")
	}
}

// cancelingObservation is a controlled runtime, not an engine or I/O probe.
type cancelingObservation struct {
	controlledRuntime
	cancel context.CancelFunc
}

func (r *cancelingObservation) Observe() (domain.State, error) {
	r.cancel()
	return r.controlledRuntime.Observe()
}

func TestCanceledPageDoesNotReturnSuccessfulPartialResults(t *testing.T) {
	for _, size := range []int{1, 2} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			first := &cancelingObservation{controlledRuntime: controlledRuntime{state: domain.Running}, cancel: cancel}
			second := &controlledRuntime{state: domain.Running}
			service := services.NewLifecycleService(map[domain.InstanceID]*services.Lifecycle{
				"a": services.NewLifecycle(domain.Instance{ID: "a"}, first),
				"b": services.NewLifecycle(domain.Instance{ID: "b"}, second),
			})
			page, err := service.ListInstanceStatuses(ctx, domain.StatusPageInput{PageSize: size})
			if !errors.Is(err, context.Canceled) || len(page.Items) != 0 || page.NextAfterID != "" {
				t.Fatalf("partial page was presented as success: %+v, %v", page, err)
			}
			if first.observations != 1 || second.observations != 0 {
				t.Fatal("page continued after cancellation")
			}
		})
	}
}
