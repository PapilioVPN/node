package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type memoryLease struct{ value domain.LeaseRecord }

func (m *memoryLease) LoadLease(context.Context) (domain.LeaseRecord, error) { return m.value, nil }
func (m *memoryLease) SaveLease(_ context.Context, r domain.LeaseRecord) error {
	m.value = r
	return nil
}
func TestLeaseRevisionTokenExpiryAndReplacementFence(t *testing.T) {
	for _, policy := range []string{"keep_running", "stop"} {
		t.Run(policy, func(t *testing.T) {
			runtime := &runtimeFixture{}
			store := &memoryLease{}
			s, err := New([]Binding{{ID: "one", Engine: "xray", Runtime: runtime, Leases: store}})
			if err != nil {
				t.Fatal(err)
			}
			runtime.execution = domain.Execution{State: domain.Running, ExecutionID: strings.Repeat("a", 32)}
			ctx := context.Background()
			receipt, err := s.AcquireLease(ctx, "one", domain.AcquireLease{ExecutionID: runtime.execution.ExecutionID, Owner: "controller", TTLSeconds: 1, Policy: policy})
			if err != nil {
				t.Fatal(err)
			}
			if len(receipt.Token) != 64 || receipt.Status.Revision != 1 {
				t.Fatal(receipt)
			}
			if _, err := s.RenewLease(ctx, "one", domain.RenewLease{ExpectedRevision: 1, Token: strings.Repeat("0", 64), TTLSeconds: 1}); !errors.Is(err, domain.ErrConflict) {
				t.Fatal("wrong token", err)
			}
			renewed, err := s.RenewLease(ctx, "one", domain.RenewLease{ExpectedRevision: 1, Token: receipt.Token, TTLSeconds: 2})
			if err != nil || renewed.Revision != 2 {
				t.Fatal(renewed, err)
			}
			if _, err := s.ReleaseLease(ctx, "one", domain.ReleaseLease{ExpectedRevision: 1, Token: receipt.Token}); !errors.Is(err, domain.ErrConflict) {
				t.Fatal("stale revision", err)
			}
			past := time.Now().Add(-time.Second)
			store.value.Status.ExpiresAt = &past
			s.entries["one"].lease = nil
			if err := s.expireLease(ctx, s.entries["one"]); err != nil {
				t.Fatal(err)
			}
			if store.value.Status.State != "expired" {
				t.Fatal(store.value)
			}
			if policy == "stop" && runtime.execution.State != domain.Stopped {
				t.Fatal("expired lease did not stop")
			}
			if policy == "keep_running" && runtime.execution.State != domain.Running {
				t.Fatal("lease killed execution")
			}
			runtime.execution = domain.Execution{State: domain.Running, ExecutionID: strings.Repeat("b", 32)}
			store.value.Status.State = "active"
			s.entries["one"].lease = nil
			if err := s.expireLease(ctx, s.entries["one"]); err != nil {
				t.Fatal(err)
			}
			if runtime.execution.State != domain.Running || store.value.Status.Error != "execution_changed" {
				t.Fatal("stale lease affected replacement")
			}
		})
	}
}

type slowLeaseStop struct {
	*runtimeFixture
	entered chan struct{}
}

func (r *slowLeaseStop) Stop(ctx context.Context) error {
	close(r.entered)
	<-ctx.Done()
	return ctx.Err()
}
func TestLeaseExpiryDoesNotDelayIndependentInstance(t *testing.T) {
	ctx := context.Background()
	expiry := time.Now().Add(100 * time.Millisecond)
	makeStore := func(id string) *memoryLease {
		return &memoryLease{value: domain.LeaseRecord{Token: strings.Repeat("c", 64), Status: domain.LeaseStatus{Revision: 1, State: "active", Owner: "fixture", ExecutionID: id, ExpiresAt: &expiry, Policy: "stop", StopEffect: domain.NotApplied}}}
	}
	slow := &slowLeaseStop{runtimeFixture: &runtimeFixture{execution: domain.Execution{State: domain.Running, ExecutionID: strings.Repeat("a", 32)}}, entered: make(chan struct{})}
	fast := &runtimeFixture{execution: domain.Execution{State: domain.Running, ExecutionID: strings.Repeat("b", 32)}}
	s, _ := New([]Binding{{ID: "slow", Engine: "xray", Runtime: slow, Leases: makeStore(slow.Observe().ExecutionID)}, {ID: "fast", Engine: "xray", Runtime: fast, Leases: makeStore(fast.Observe().ExecutionID)}})
	defer s.Close(ctx)
	if err := s.StartLeases(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-slow.entered:
	case <-time.After(time.Second):
		t.Fatal("expiry did not start")
	}
	deadline := time.After(300 * time.Millisecond)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for fast.Observe().State == domain.Running {
		select {
		case <-deadline:
			t.Fatal("slow stop delayed independent lease")
		case <-tick.C:
		}
	}
}
