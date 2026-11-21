package services_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type execution struct {
	mu         sync.Mutex
	generation uint64
}

func (e *execution) Status(ctx context.Context, _ string) (instances.Status, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: e.generation}}, ctx.Err()
}
func (e *execution) next() { e.mu.Lock(); defer e.mu.Unlock(); e.generation++ }

type native struct {
	add  func(context.Context) (domain.NativeResult, error)
	read func(context.Context) error
}

func (n native) Add(ctx context.Context, _ []byte) (domain.NativeResult, error)    { return n.add(ctx) }
func (n native) Remove(ctx context.Context, _ string) (domain.NativeResult, error) { return n.add(ctx) }
func (n native) Records(ctx context.Context) ([]domain.Record, error)              { return nil, n.read(ctx) }
func (n native) Tags(ctx context.Context) ([]string, error) {
	return []string{"", "duplicate", "duplicate"}, n.read(ctx)
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
	if _, err := s.Tags(context.Background(), "edge-a"); !errors.Is(err, domain.ErrUncertain) {
		t.Fatal(err)
	}
	state, err := s.Status(context.Background(), "edge-a")
	if err != nil || !state.RequiresInstanceRestart || state.LastOperation == nil || state.LastOperation.Effect != domain.Unknown || calls != 1 {
		t.Fatal(state, err, calls)
	}
	e.next()
	tags, err := s.Tags(context.Background(), "edge-a")
	if err != nil || len(tags.Tags) != 3 || calls != 2 {
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
func TestBoundedReadersRejectConflictingMutationWithoutQueue(t *testing.T) {
	e := &execution{generation: 1}
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	s := service(t, native{read: func(ctx context.Context) error {
		entered <- struct{}{}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, add: func(context.Context) (domain.NativeResult, error) {
		t.Error("conflicting mutation submitted")
		return domain.NativeResult{}, nil
	}}, e)
	results := make(chan error, 8)
	for range 8 {
		go func() { _, err := s.Tags(context.Background(), "edge-a"); results <- err }()
	}
	for range 8 {
		<-entered
	}
	if _, err := s.Tags(context.Background(), "edge-a"); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	if _, err := s.Remove(context.Background(), "edge-a", "a"); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	state, err := s.Status(context.Background(), "edge-a")
	if err != nil || state.RequestInFlight {
		t.Fatal("metadata blocked by I/O", state, err)
	}
	once.Do(func() { close(release) })
	for range 8 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}
func TestInvalidAndUnsupportedRequestsHaveNoEffect(t *testing.T) {
	e := &execution{generation: 1}
	s := service(t, native{add: func(context.Context) (domain.NativeResult, error) {
		t.Fatal("unexpected native call")
		return domain.NativeResult{}, nil
	}}, e)
	for _, content := range [][]byte{nil, make([]byte, domain.MaxConfigurationBytes+1)} {
		result, err := s.Add(context.Background(), "edge-a", content)
		if err == nil || result.Effect != domain.NotApplied {
			t.Fatal(result, err)
		}
	}
	for _, tag := range []string{"", "a\x00b", string([]byte{255})} {
		if _, err := s.Remove(context.Background(), "edge-a", tag); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatal(err)
		}
	}
	if _, err := s.Tags(context.Background(), "no-api"); !errors.Is(err, domain.ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := s.Tags(context.Background(), "unknown"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
}
