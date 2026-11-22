package services_test

import (
	"context"
	"errors"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

func TestLookupRetainsAbsenceAndExactSelection(t *testing.T) {
	calls := 0
	c := client{get: func(_ context.Context, tag, email string) (*domain.Record, error) {
		calls++
		if tag != "members / a" || email != "MixedCase@example.test" {
			t.Fatal(tag, email)
		}
		return nil, nil
	}}
	s, err := services.New([]services.Binding{{ID: "a", Client: c}}, &execution{generation: 7, state: instances.Running})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Get(context.Background(), "a", "members / a", "MixedCase@example.test")
	if err != nil || result.User != nil || result.Generation != 7 || result.RequestedEmail != "MixedCase@example.test" || result.ObservedAt.IsZero() || calls != 1 {
		t.Fatal(result, err, calls)
	}
	for _, email := range []string{"", "nul\x00@example.test"} {
		if _, err := s.Get(context.Background(), "a", "members / a", email); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal("invalid selection reached native IO")
	}
}
func TestCountFencesReplacementAndPreservesFailure(t *testing.T) {
	e := &execution{generation: 1, state: instances.Running}
	cause := errors.New("native failure")
	s, err := services.New([]services.Binding{{ID: "a", Client: client{count: func(context.Context, string) (int64, error) { e.replace(); return 2, cause }}}}, e)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Count(context.Background(), "a", "members")
	if !errors.Is(err, domain.ErrExecutionChanged) || !errors.Is(err, cause) || result.Generation != 0 {
		t.Fatal(result, err)
	}
}
func TestNonrunningOrUnconfiguredReadsNeverCallNative(t *testing.T) {
	e := &execution{generation: 1, state: instances.Stopped}
	s, err := services.New([]services.Binding{{ID: "a", Client: client{count: func(context.Context, string) (int64, error) { t.Fatal("stopped instance read"); return 0, nil }}}, {ID: "b"}}, e)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		id   string
		want error
	}{{"a", domain.ErrNotRunning}, {"b", domain.ErrUnsupported}, {"missing", domain.ErrNotFound}, {"", domain.ErrInvalidArgument}} {
		if _, err := s.Count(context.Background(), test.id, "members"); !errors.Is(err, test.want) {
			t.Fatal(test.id, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Count(ctx, "a", "members"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestReadAdmissionRejectsAndRecoversAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered := make(chan struct{}, 32)
	done := make(chan error, 32)
	c := client{count: func(ctx context.Context, _ string) (int64, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return 0, ctx.Err()
	}}
	var bindings []services.Binding
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		bindings = append(bindings, services.Binding{ID: id, Client: c})
	}
	s, err := services.New(bindings, &execution{generation: 1, state: instances.Running})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		for range 8 {
			go func() { _, err := s.Count(ctx, id, "members"); done <- err }()
		}
	}
	for range 32 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("native calls failed to enter", ctx.Err())
		}
	}
	for _, id := range []string{"a", "e"} {
		if _, err := s.Count(context.Background(), id, "members"); !errors.Is(err, domain.ErrCapacity) {
			t.Fatal(id, err)
		}
	}
	cancel()
	for range 32 {
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	next, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer stop()
	if _, err := s.Count(next, "a", "members"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("admission was not released", err)
	}
}
