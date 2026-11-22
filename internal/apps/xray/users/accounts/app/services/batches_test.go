package services_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

type preparedOnlyClient struct {
	client
	prepared, submitted int
}

func (c *preparedOnlyClient) Prepare([]byte) (domain.PreparedAccount, error) {
	c.prepared++
	return nativeSourceAccount{kind: "test.account", submit: func(context.Context, string) (domain.NativeResult, error) {
		c.submitted++
		return domain.NativeResult{Effect: domain.Completed, Acknowledged: true}, nil
	}}, nil
}

func (*preparedOnlyClient) Add(context.Context, string, []byte) (domain.NativeResult, error) {
	panic("accepted account was decoded again for submission")
}

func TestRawAdditionsUsePreparedAccountForCompatibilityAndSubmission(t *testing.T) {
	for _, size := range []int{1, 50, 51, 1000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			c := &preparedOnlyClient{}
			s, _ := newService([]services.Binding{{ID: "a", Client: c}}, &execution{generation: 1, state: instances.Running})
			items := make([]domain.Addition, size)
			for i := range items {
				items[i] = domain.Addition{InboundTag: "members", Configuration: []byte{1}}
			}
			result, err := s.AddMany(context.Background(), "a", items, 1)
			if err != nil || result.Operation.Effect != domain.Completed || c.prepared != size || c.submitted != size {
				t.Fatal(result.Operation, err, c.prepared, c.submitted)
			}
			if _, err := s.Add(context.Background(), "a", "members", []byte{1}); err != nil || c.prepared != size+1 || c.submitted != size+1 {
				t.Fatal(err, c.prepared, c.submitted)
			}
		})
	}
}

func TestBatchesPreserveSameInboundOrderWithoutPreflightReads(t *testing.T) {
	for _, size := range []int{1, 50, 51, 1000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			calls := 0
			c := client{remove: func(_ context.Context, tag, email string) (domain.NativeResult, error) {
				if tag != "members" || email != fmt.Sprint(calls) {
					t.Fatal("native order changed", tag, email, calls)
				}
				calls++
				return domain.NativeResult{Effect: domain.Completed, Acknowledged: true}, nil
			}}
			s, _ := newService([]services.Binding{{ID: "a", Client: c}}, &execution{generation: 1, state: instances.Running})
			items := make([]domain.Removal, size)
			for i := range items {
				items[i] = domain.Removal{InboundTag: "members", Email: fmt.Sprint(i)}
			}
			result, err := s.RemoveMany(context.Background(), "a", items, 1)
			if err != nil || calls != size || len(result.Items) != size || result.Operation.Effect != domain.Completed || !result.Operation.Acknowledged {
				t.Fatal(result.Operation, err, calls)
			}
			for i, item := range result.Items {
				if item.Index != i || !item.Attempted || item.Err != nil || item.Operation.Email != fmt.Sprint(i) || item.Operation.Generation != 1 {
					t.Fatal(item)
				}
			}
		})
	}
}

func TestBatchOverlapsSameInboundAccountsAndExcludesConflicts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	entered := make(chan string, 8)
	release := make(chan struct{})
	var active, maximum atomic.Int32
	c := client{remove: func(ctx context.Context, tag, _ string) (domain.NativeResult, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
		}
		entered <- tag
		select {
		case <-release:
			return domain.NativeResult{Effect: domain.Completed, Acknowledged: true}, nil
		case <-ctx.Done():
			return domain.NativeResult{Effect: domain.Unknown}, ctx.Err()
		}
	}}
	s, _ := newService([]services.Binding{{ID: "a", Client: c}}, &execution{generation: 1, state: instances.Running})
	var items []domain.Removal
	for i := range 8 {
		items = append(items, domain.Removal{InboundTag: "members", Email: fmt.Sprint(i)})
	}
	done := make(chan error, 1)
	go func() { _, err := s.RemoveMany(ctx, "a", items, 0); done <- err }()
	for range 4 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("four independent inbounds did not overlap", ctx.Err())
		}
	}
	if _, err := s.Remove(context.Background(), "a", "members", "x"); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	if _, err := s.Count(context.Background(), "a", "members"); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	if status, err := s.Status(context.Background(), "a"); err != nil || !status.RequestInFlight {
		t.Fatal(status, err)
	}
	close(release)
	if err := <-done; err != nil || maximum.Load() != 4 || active.Load() != 0 {
		t.Fatal(err, maximum.Load(), active.Load())
	}
}

func TestBatchStopsNewWorkButReportsAndJoinsInFlightEffects(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	second := make(chan struct{})
	finish := make(chan struct{})
	failure := errors.New("native rejection")
	c := client{remove: func(_ context.Context, tag, _ string) (domain.NativeResult, error) {
		if tag == "first" {
			<-second
			return domain.NativeResult{Effect: domain.Unknown, RequiresInstanceRestart: true}, failure
		}
		close(second)
		<-finish
		return domain.NativeResult{Effect: domain.Completed, Acknowledged: true}, nil
	}}
	s, _ := newService([]services.Binding{{ID: "a", Client: c}}, &execution{generation: 1, state: instances.Running})
	done := make(chan domain.Batch, 1)
	go func() {
		result, err := s.RemoveMany(ctx, "a", []domain.Removal{{InboundTag: "first"}, {InboundTag: "second"}}, 2)
		if !errors.Is(err, failure) {
			t.Error(err)
		}
		done <- result
	}()
	select {
	case <-second:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-done:
		t.Fatal("batch returned before joining an in-flight effect")
	default:
	}
	close(finish)
	result := <-done
	if result.Operation.Effect != domain.Unknown || !result.Operation.RequiresInstanceRestart || result.Items[0].Operation.Effect != domain.Unknown || !result.Items[1].Attempted || result.Items[1].Operation.Effect != domain.Completed {
		t.Fatal(result)
	}
	if _, err := s.Count(context.Background(), "a", "members"); !errors.Is(err, domain.ErrUncertain) {
		t.Fatal("lost effect did not fence the execution", err)
	}
}

func TestBatchGenerationChangeAndInvalidShapesSubmitNoFurtherEffects(t *testing.T) {
	e := &execution{generation: 1, state: instances.Running}
	calls := 0
	c := client{add: func(context.Context, string, []byte) (domain.NativeResult, error) {
		calls++
		e.replace()
		return domain.NativeResult{Effect: domain.Completed, Acknowledged: true}, nil
	}}
	s, _ := newService([]services.Binding{{ID: "a", Client: c}}, e)
	for _, input := range [][]domain.Addition{nil, {{InboundTag: "a", Configuration: []byte{1}}, {InboundTag: "", Configuration: []byte{1}}}, make([]domain.Addition, domain.MaxBatchItems+1)} {
		if _, err := s.AddMany(context.Background(), "a", input, 1); err == nil || calls != 0 {
			t.Fatal(err, calls)
		}
	}
	result, err := s.AddMany(context.Background(), "a", []domain.Addition{{InboundTag: "members", Configuration: []byte{1}}, {InboundTag: "members", Configuration: []byte{2}}}, 1)
	if !errors.Is(err, domain.ErrExecutionChanged) || calls != 1 || result.Items[0].Operation.Effect != domain.Unknown || !result.Items[0].Operation.Acknowledged || result.Items[1].Attempted {
		t.Fatal(result, err, calls)
	}
}

func TestBatchSharesEightGlobalNativeMutationSlots(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	entered := make(chan struct{}, 8)
	var running sync.WaitGroup
	c := client{remove: func(ctx context.Context, _, _ string) (domain.NativeResult, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return domain.NativeResult{Effect: domain.Unknown, RequiresInstanceRestart: true}, ctx.Err()
	}}
	s, _ := newService([]services.Binding{{ID: "a", Client: c}, {ID: "b", Client: c}, {ID: "c", Client: c}}, &execution{generation: 1, state: instances.Running})
	items := []domain.Removal{{InboundTag: "a"}, {InboundTag: "b"}, {InboundTag: "c"}, {InboundTag: "d"}}
	for _, id := range []string{"a", "b"} {
		running.Go(func() { _, _ = s.RemoveMany(ctx, id, items, 0) })
		for range 4 {
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
	}
	if _, err := s.Remove(context.Background(), "c", "a", ""); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	cancel()
	running.Wait()
}

func TestOrderedBatchReportsKnownPartialEffectsAndUnattemptedTail(t *testing.T) {
	calls := 0
	c := client{add: func(context.Context, string, []byte) (domain.NativeResult, error) {
		calls++
		if calls == 2 {
			return domain.NativeResult{Effect: domain.NotApplied}, domain.ErrInvalidArgument
		}
		return domain.NativeResult{Email: "first", Effect: domain.Completed, Acknowledged: true}, nil
	}}
	s, _ := newService([]services.Binding{{ID: "a", Client: c}}, &execution{generation: 1, state: instances.Running})
	items := []domain.Addition{{InboundTag: "members", Configuration: []byte{1}}, {InboundTag: "members", Configuration: []byte{255}}, {InboundTag: "members", Configuration: []byte{2}}}
	result, err := s.AddMany(context.Background(), "a", items, 1)
	if !errors.Is(err, domain.ErrInvalidArgument) || calls != 2 || result.Operation.Effect != domain.Partial || result.Operation.Acknowledged || result.Items[0].Operation.Effect != domain.Completed || result.Items[1].Operation.Effect != domain.NotApplied || result.Items[2].Attempted || !errors.Is(result.Items[2].Err, domain.ErrNotAttempted) {
		t.Fatal(result, calls, err)
	}
	status, err := s.Status(context.Background(), "a")
	if err != nil || status.RequestInFlight || status.RequiresInstanceRestart || status.LastOperation == nil || status.LastOperation.Kind != "add_batch" || status.LastOperation.Effect != domain.Partial {
		t.Fatal(status, err)
	}
}

func TestBatchCancellationJoinsAllActiveNativeWork(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	entered := make(chan struct{}, 4)
	var active atomic.Int32
	c := client{remove: func(ctx context.Context, _, _ string) (domain.NativeResult, error) {
		active.Add(1)
		defer active.Add(-1)
		entered <- struct{}{}
		<-ctx.Done()
		return domain.NativeResult{Effect: domain.Unknown, RequiresInstanceRestart: true}, ctx.Err()
	}}
	s, _ := newService([]services.Binding{{ID: "a", Client: c}}, &execution{generation: 1, state: instances.Running})
	done := make(chan error, 1)
	go func() {
		_, err := s.RemoveMany(ctx, "a", []domain.Removal{{InboundTag: "a"}, {InboundTag: "a"}, {InboundTag: "a"}, {InboundTag: "a"}, {InboundTag: "a"}}, 4)
		done <- err
	}()
	for range 4 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) || active.Load() != 0 {
		t.Fatal(err, active.Load())
	}
	status, _ := s.Status(context.Background(), "a")
	if status.RequestInFlight || !status.RequiresInstanceRestart {
		t.Fatal(status)
	}
}

func TestBatchByteAndParallelismBoundsRejectBeforeNativeWork(t *testing.T) {
	calls := 0
	c := client{add: func(context.Context, string, []byte) (domain.NativeResult, error) {
		calls++
		return domain.NativeResult{}, nil
	}}
	s, _ := newService([]services.Binding{{ID: "a", Client: c}}, &execution{generation: 1, state: instances.Running})
	large := make([]byte, domain.MaxAccountBytes)
	items := []domain.Addition{{InboundTag: "a", Configuration: large}, {InboundTag: "a", Configuration: large}, {InboundTag: "a", Configuration: large}, {InboundTag: "a", Configuration: large}, {InboundTag: "a", Configuration: []byte{1}}}
	if _, err := s.AddMany(context.Background(), "a", items, 1); !errors.Is(err, domain.ErrCapacity) || calls != 0 {
		t.Fatal(err, calls)
	}
	for _, parallelism := range []int{-1, domain.MaxBatchParallelism + 1} {
		if _, err := s.AddMany(context.Background(), "a", items[:1], parallelism); !errors.Is(err, domain.ErrInvalidArgument) || calls != 0 {
			t.Fatal(err, calls)
		}
	}
}
