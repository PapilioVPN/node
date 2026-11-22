package services_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

func TestLostAccountMutationFencesReadsAndWritesUntilReplacement(t *testing.T) {
	e := &execution{generation: 1, state: instances.Running}
	calls := 0
	c := client{
		add: func(context.Context, string, []byte) (domain.NativeResult, error) {
			calls++
			return domain.NativeResult{Email: "a", Effect: domain.Unknown, RequiresInstanceRestart: true}, context.DeadlineExceeded
		},
		count: func(context.Context, string) (int64, error) { calls++; return 2, nil },
	}
	s, err := services.New([]services.Binding{{ID: "a", Client: c}}, e)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Add(context.Background(), "a", "members", []byte{1})
	if !errors.Is(err, context.DeadlineExceeded) || result.Effect != domain.Unknown || !result.RequiresInstanceRestart {
		t.Fatal(result, err)
	}
	result, err = s.Remove(context.Background(), "a", "members", "b")
	if !errors.Is(err, domain.ErrUncertain) || result.Effect != domain.NotApplied || !result.RequiresInstanceRestart {
		t.Fatal(result, err)
	}
	if _, err := s.Count(context.Background(), "a", "members"); !errors.Is(err, domain.ErrUncertain) {
		t.Fatal(err)
	}
	state, err := s.Status(context.Background(), "a")
	if err != nil || state.LastOperation == nil || state.LastOperation.Email != "a" || !state.RequiresInstanceRestart || state.RequestInFlight || calls != 1 {
		t.Fatal(state, err, calls)
	}
	e.replace()
	if _, err := s.Count(context.Background(), "a", "members"); err != nil {
		t.Fatal(err)
	}
	state, err = s.Status(context.Background(), "a")
	if err != nil || state.RequiresInstanceRestart || state.LastOperation.Generation != 1 || state.Generation != 2 || calls != 2 {
		t.Fatal(state, err, calls)
	}
}
func TestAccountMutationBlocksConflictsWithoutBlockingLocalStatus(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	entered := make(chan struct{})
	done := make(chan error, 1)
	c := client{add: func(ctx context.Context, _ string, _ []byte) (domain.NativeResult, error) {
		close(entered)
		<-ctx.Done()
		return domain.NativeResult{Effect: domain.Unknown, RequiresInstanceRestart: true}, ctx.Err()
	}}
	s, err := services.New([]services.Binding{{ID: "a", Client: c}}, &execution{generation: 1, state: instances.Running})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, err := s.Add(ctx, "a", "members", []byte{1}); done <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := s.Count(context.Background(), "a", "members"); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	if _, err := s.Remove(context.Background(), "a", "members", "b"); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	state, err := s.Status(context.Background(), "a")
	if err != nil || !state.RequestInFlight {
		t.Fatal(state, err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestAccountGenerationChangeDoesNotClaimCurrentApplication(t *testing.T) {
	e := &execution{generation: 1, state: instances.Running}
	c := client{remove: func(context.Context, string, string) (domain.NativeResult, error) {
		e.replace()
		return domain.NativeResult{Effect: domain.Completed, Acknowledged: true}, nil
	}}
	s, err := services.New([]services.Binding{{ID: "a", Client: c}}, e)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Remove(context.Background(), "a", "members", "b")
	if !errors.Is(err, domain.ErrExecutionChanged) || result.Effect != domain.Unknown || !result.Acknowledged || result.Generation != 1 {
		t.Fatal(result, err)
	}
}

func TestGlobalAccountMutationCapacityIsBounded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	entered := make(chan struct{}, 8)
	done := make(chan error, 8)
	c := client{remove: func(ctx context.Context, _ string, _ string) (domain.NativeResult, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return domain.NativeResult{Effect: domain.Unknown, RequiresInstanceRestart: true}, ctx.Err()
	}}
	var bindings []services.Binding
	for i := range 9 {
		bindings = append(bindings, services.Binding{ID: fmt.Sprintf("instance-%d", i), Client: c})
	}
	s, err := services.New(bindings, &execution{generation: 1, state: instances.Running})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 8 {
		go func() { _, err := s.Remove(ctx, fmt.Sprintf("instance-%d", i), "members", "a"); done <- err }()
	}
	for range 8 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("mutation admission did not fill", ctx.Err())
		}
	}
	if result, err := s.Remove(context.Background(), "instance-8", "members", "a"); !errors.Is(err, domain.ErrCapacity) || result.Effect != domain.NotApplied {
		t.Fatal(result, err)
	}
	cancel()
	for range 8 {
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}
