package services

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

func TestUnresolvedRemoteCallBlocksReplayUntilExecutionChanges(t *testing.T) {
	generation := uint64(1)
	calls := 0
	service := serviceFixture(t, func(context.Context) (domain.NativeResult, error) {
		calls++
		return domain.NativeResult{Effect: domain.Unknown, RequiresInstanceRestart: true}, context.DeadlineExceeded
	}, statusFunc(func(ctx context.Context, id string) (instances.Status, error) {
		result, _ := running(ctx, id)
		result.Execution.Generation = generation
		return result, nil
	}))
	result, err := service.Reopen(context.Background(), "a")
	if !errors.Is(err, context.DeadlineExceeded) || result.Effect != domain.Unknown || !result.RequiresInstanceRestart || result.Acknowledged || result.ObservedAt.IsZero() {
		t.Fatalf("lost unknown outcome: %+v %v", result, err)
	}
	state, err := service.Status(context.Background(), "a")
	if err != nil || !state.RequiresInstanceRestart || state.RequestInFlight || state.LastOperation == nil {
		t.Fatalf("lost recovery status: %+v %v", state, err)
	}
	state.LastOperation.Effect = domain.Completed
	if result, err := service.Reopen(context.Background(), "a"); !errors.Is(err, domain.ErrUncertain) || result.Effect != domain.NotApplied || !result.RequiresInstanceRestart || calls != 1 {
		t.Fatal("uncertain execution admitted another remote mutation", err)
	}
	state, _ = service.Status(context.Background(), "a")
	if state.LastOperation.Effect != domain.Unknown {
		t.Fatal("status caller mutated owned evidence")
	}
	generation = 2
	service.entries["a"].restarter = restartFunc(func(context.Context) (domain.NativeResult, error) {
		calls++
		return domain.NativeResult{Effect: domain.Completed, Acknowledged: true}, nil
	})
	result, err = service.Reopen(context.Background(), "a")
	if err != nil || result.Effect != domain.Completed || !result.Acknowledged || result.Generation != 2 || result.RequiresInstanceRestart || calls != 2 {
		t.Fatalf("new execution remained fenced %+v %v", result, err)
	}
}
func TestTerminalNativeFailureCanBeExplicitlyRetried(t *testing.T) {
	cause := errors.New("private failed start")
	service := serviceFixture(t, func(context.Context) (domain.NativeResult, error) {
		return domain.NativeResult{Effect: domain.Unknown}, cause
	}, statusFunc(running))
	result, err := service.Reopen(context.Background(), "a")
	if !errors.Is(err, cause) || result.RequiresInstanceRestart || result.Effect != domain.Unknown {
		t.Fatalf("native error became no effect: %+v %v", result, err)
	}
	service.entries["a"].restarter = restartFunc(func(context.Context) (domain.NativeResult, error) {
		return domain.NativeResult{Effect: domain.Completed, Acknowledged: true}, nil
	})
	if result, err := service.Reopen(context.Background(), "a"); err != nil || !result.Acknowledged {
		t.Fatal("settled failure prevented explicit correction", err)
	}
}
func TestAcknowledgementAndChangedExecutionRemainIndependent(t *testing.T) {
	calls := 0
	service := serviceFixture(t, func(context.Context) (domain.NativeResult, error) {
		return domain.NativeResult{Effect: domain.Completed, Acknowledged: true}, nil
	}, statusFunc(func(ctx context.Context, id string) (instances.Status, error) {
		calls++
		result, _ := running(ctx, id)
		if calls == 2 {
			result.Execution.Generation++
		}
		return result, nil
	}))
	result, err := service.Reopen(context.Background(), "a")
	if !errors.Is(err, domain.ErrExecutionChanged) || result.Effect != domain.Unknown || !result.Acknowledged || result.RequiresInstanceRestart {
		t.Fatalf("mixed execution hid acknowledgement: %+v %v", result, err)
	}
}
func TestAdmissionAndReadbackDuringMutation(t *testing.T) {
	entered := make(chan struct{})
	done := make(chan error, 1)
	service := serviceFixture(t, func(ctx context.Context) (domain.NativeResult, error) {
		close(entered)
		<-ctx.Done()
		return domain.NativeResult{Effect: domain.Unknown, RequiresInstanceRestart: true}, ctx.Err()
	}, statusFunc(running))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { _, err := service.Reopen(ctx, "a"); done <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if result, err := service.Reopen(ctx, "a"); !errors.Is(err, domain.ErrCapacity) || result.Effect != domain.NotApplied {
		t.Fatal("overlapping logger requests were admitted", err)
	}
	state, err := service.Status(ctx, "a")
	if err != nil || !state.RequestInFlight {
		t.Fatal("readback blocked behind logger I/O", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestRejectedSelectionsNeverReachNativeLogger(t *testing.T) {
	service := serviceFixture(t, func(context.Context) (domain.NativeResult, error) {
		t.Error("rejected selection reached native logger")
		return domain.NativeResult{}, nil
	}, statusFunc(func(context.Context, string) (instances.Status, error) { return instances.Status{}, nil }))
	for _, test := range []struct {
		id   string
		want error
	}{{"missing", domain.ErrNotFound}, {"../bad", domain.ErrInvalidArgument}, {"disabled", domain.ErrUnsupported}, {"a", domain.ErrNotRunning}} {
		result, err := service.Reopen(context.Background(), test.id)
		if !errors.Is(err, test.want) || result.Effect != domain.NotApplied {
			t.Fatal(test.id, err)
		}
	}
}

func TestGlobalLoggerCapacityRejectsWithoutQueueingNativeWork(t *testing.T) {
	entered := make(chan struct{}, 8)
	restarter := restartFunc(func(ctx context.Context) (domain.NativeResult, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return domain.NativeResult{Effect: domain.Unknown, RequiresInstanceRestart: true}, ctx.Err()
	})
	var bindings []Binding
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		bindings = append(bindings, Binding{ID: id, Restarter: restarter})
	}
	service, err := New(bindings, statusFunc(running))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var joined sync.WaitGroup
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		joined.Go(func() { service.Reopen(ctx, id) })
	}
	for range 8 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if result, err := service.Reopen(ctx, "i"); !errors.Is(err, domain.ErrCapacity) || result.Effect != domain.NotApplied {
		t.Fatal("global logger work queued", err)
	}
	cancel()
	joined.Wait()
	if len(service.slots) != 0 {
		t.Fatal("logger admission leaked after cancellation")
	}
}

func TestRetainedLoggerEvidenceOwnsInstanceID(t *testing.T) {
	id := []byte("a")
	borrowed := unsafe.String(unsafe.SliceData(id), len(id))
	s := serviceFixture(t, func(context.Context) (domain.NativeResult, error) {
		return domain.NativeResult{Effect: domain.Completed, Acknowledged: true}, nil
	}, statusFunc(running))
	if _, err := s.Reopen(context.Background(), borrowed); err != nil {
		t.Fatal(err)
	}
	id[0] = 'x'
	state, err := s.Status(context.Background(), "a")
	if err != nil || state.LastOperation == nil || state.LastOperation.InstanceID != "a" {
		t.Fatal("retained evidence borrowed request memory", state.LastOperation, err)
	}
}
