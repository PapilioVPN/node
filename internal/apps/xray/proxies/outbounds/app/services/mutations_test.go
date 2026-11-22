package services_test

import (
	"context"
	"errors"
	"testing"
	"unsafe"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
)

type native struct {
	add  func(context.Context) (domain.NativeResult, error)
	read func(context.Context) error
}

func (n native) Add(ctx context.Context, _ []byte) (domain.NativeResult, error)    { return n.add(ctx) }
func (n native) Remove(ctx context.Context, _ string) (domain.NativeResult, error) { return n.add(ctx) }
func (n native) Records(ctx context.Context) ([]domain.Record, error) {
	return []domain.Record{{}, {}, {}}, n.read(ctx)
}

func service(t *testing.T, n native, e *execution) *services.Service {
	t.Helper()
	s, err := services.New([]services.Binding{{ID: "edge-a", Client: n}, {ID: "no-api"}}, e)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestUnresolvedMutationFencesNativeReadsAndWritesUntilReplacement(t *testing.T) {
	e := &execution{generation: 1}
	calls := 0
	n := native{add: func(context.Context) (domain.NativeResult, error) {
		calls++
		return domain.NativeResult{Tag: "a", Effect: domain.Unknown, RequiresInstanceRestart: true}, context.DeadlineExceeded
	}, read: func(context.Context) error { calls++; return nil }}
	s := service(t, n, e)
	result, err := s.Add(context.Background(), "edge-a", []byte{1})
	if !errors.Is(err, context.DeadlineExceeded) || result.Effect != domain.Unknown || !result.RequiresInstanceRestart {
		t.Fatal(result, err)
	}
	result, err = s.Remove(context.Background(), "edge-a", "a")
	if !errors.Is(err, domain.ErrUncertain) || result.Effect != domain.NotApplied || !result.RequiresInstanceRestart {
		t.Fatal(result, err)
	}
	if _, err := s.Records(context.Background(), "edge-a"); !errors.Is(err, domain.ErrUncertain) {
		t.Fatal(err)
	}
	state, err := s.Status(context.Background(), "edge-a")
	if err != nil || !state.RequiresInstanceRestart || state.LastOperation == nil || state.LastOperation.Effect != domain.Unknown || calls != 1 {
		t.Fatal(state, err, calls)
	}
	e.next()
	tags, err := s.Records(context.Background(), "edge-a")
	if err != nil || len(tags.Outbounds) != 3 || calls != 2 {
		t.Fatal(tags, err, calls)
	}
	state, err = s.Status(context.Background(), "edge-a")
	if err != nil || state.RequiresInstanceRestart || state.LastOperation.Generation != 1 {
		t.Fatal(state, err)
	}
}
func TestGenerationChangePreservesAcknowledgementWithoutClaimingCurrentApplication(t *testing.T) {
	e := &execution{generation: 1}
	s := service(t, native{add: func(context.Context) (domain.NativeResult, error) {
		e.next()
		return domain.NativeResult{Tag: "a", Effect: domain.Completed, Acknowledged: true}, nil
	}}, e)
	result, err := s.Add(context.Background(), "edge-a", []byte{1})
	if !errors.Is(err, domain.ErrExecutionChanged) || !result.Acknowledged || result.Effect != domain.Unknown || result.Generation != 1 {
		t.Fatal(result, err)
	}
}

func TestMutationRejectsConcurrentReadAndWriteWithoutBlockingStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	done := make(chan error, 1)
	s := service(t, native{add: func(ctx context.Context) (domain.NativeResult, error) {
		close(entered)
		<-ctx.Done()
		return domain.NativeResult{Effect: domain.Unknown, RequiresInstanceRestart: true}, ctx.Err()
	}, read: func(context.Context) error { t.Error("read dispatched during write"); return nil }}, &execution{generation: 1})
	go func() { _, err := s.Add(ctx, "edge-a", []byte{1}); done <- err }()
	<-entered
	if _, err := s.Remove(context.Background(), "edge-a", "a"); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	if _, err := s.Records(context.Background(), "edge-a"); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	state, err := s.Status(context.Background(), "edge-a")
	if err != nil || !state.RequestInFlight {
		t.Fatal(state, err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRetainedMutationEvidenceOwnsRequestSelectors(t *testing.T) {
	id, tag := []byte("edge-a"), []byte("selected")
	borrow := func(value []byte) string { return unsafe.String(unsafe.SliceData(value), len(value)) }
	s := service(t, native{add: func(context.Context) (domain.NativeResult, error) {
		return domain.NativeResult{Effect: domain.Completed, Acknowledged: true}, nil
	}}, &execution{generation: 1})
	if _, err := s.Remove(context.Background(), borrow(id), borrow(tag)); err != nil {
		t.Fatal(err)
	}
	for _, value := range [][]byte{id, tag} {
		for i := range value {
			value[i] = 'x'
		}
	}
	state, err := s.Status(context.Background(), "edge-a")
	if err != nil || state.LastOperation == nil || state.LastOperation.InstanceID != "edge-a" || state.LastOperation.Tag != "selected" {
		t.Fatal("retained evidence borrowed request memory", state.LastOperation, err)
	}
}
