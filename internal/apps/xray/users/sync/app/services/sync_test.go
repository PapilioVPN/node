package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unsafe"

	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
	codec "github.com/PapilioVPN/node/internal/apps/xray/users/sync/infra/accounts"
)

func TestApplyReplayReadbackAndUnchanged(t *testing.T) {
	for _, count := range []int{1, 50, 51, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			s, c := newControlled(t)
			ctx := context.Background()
			input := syncRequest(t, s, domain.Delta, count)
			p, err := s.Preview(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.Apply(ctx, "one", p.PlanID, p.Revision)
			if err != nil || r.Receipt.Effect != accounts.Completed {
				t.Fatalf("apply: %+v %v", r, err)
			}
			// Treat the preceding response as lost, then retrieve it by the identity
			// chosen before dispatch; no new account effects are permitted.
			replay, err := s.Apply(ctx, "one", p.PlanID, p.Revision)
			if err != nil || replay.Receipt.OperationID != input.OperationID || c.adds != count {
				t.Fatalf("replay: %+v %v calls=%d", replay, err, c.adds)
			}
			seen := 0
			offset := 0
			for {
				page, err := s.Status(ctx, "one", input.OperationID, offset)
				if err != nil || len(page.Receipt.Steps) > 50 {
					t.Fatal(page, err)
				}
				seen += len(page.Receipt.Steps)
				if page.NextOffset == 0 {
					break
				}
				offset = page.NextOffset
			}
			if seen != count {
				t.Fatal(seen)
			}
			input.OperationID = "unchanged"
			input.Expected, _ = s.Revision(ctx, "one")
			p, err = s.Preview(ctx, input)
			if err != nil || p.Differences[0].Unchanged != count {
				t.Fatal(p, err)
			}
			if _, err = s.Apply(ctx, "one", p.PlanID, p.Revision); err != nil || c.adds != count || c.removes != 0 {
				t.Fatal(err, c.adds, c.removes)
			}
			t.Logf("controlled workload=%d membership_captures=%d additions=%d removals=%d replay_mutations=0 unchanged_mutations=0", count, c.reads, c.adds, c.removes)
		})
	}
}
func TestUnknownPartialAndFailedRemoval(t *testing.T) {
	s, c := newControlled(t)
	ctx := context.Background()
	c.failAt = 2
	c.unknown = true
	p, err := s.Preview(ctx, syncRequest(t, s, domain.Delta, 3))
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Apply(ctx, "one", p.PlanID, p.Revision)
	if err == nil || r.Receipt.Effect != accounts.Unknown || !r.Receipt.RequiresRecovery || !r.Receipt.Steps[0].Acknowledged || r.Receipt.Steps[2].Attempted || c.adds != 2 {
		t.Fatalf("unknown: %+v %v", r, err)
	}
	data, _ := json.Marshal(r)
	if strings.Contains(string(data), "secret") {
		t.Fatal("private native error exposed")
	}
	if _, err = s.Apply(ctx, "one", p.PlanID, p.Revision); err == nil || c.adds != 2 {
		t.Fatal("unknown replay mutated", err)
	}
	input := syncRequest(t, s, domain.Delta, 1)
	input.OperationID = "unsafe-replay"
	if _, err = s.Preview(ctx, input); !errors.Is(err, domain.ErrUncertain) {
		t.Fatal(err)
	}
	c.generation++
	if _, err = s.Refresh(ctx, "one", "operation"); err != nil {
		t.Fatal(err)
	}
	input.Expected, _ = s.Revision(ctx, "one")
	if _, err = s.Preview(ctx, input); err != nil {
		t.Fatal(err)
	}

	s, c = newControlled(t)
	c.users["u-0000"] = user("u-0000", "old")
	c.failRemove = true
	p, err = s.Preview(ctx, syncRequest(t, s, domain.Delta, 1))
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.Apply(ctx, "one", p.PlanID, p.Revision)
	if err == nil || c.adds != 0 || c.removes != 1 || r.Receipt.Steps[1].Attempted {
		t.Fatalf("remove failure: %+v %v", r, err)
	}
}
func TestStalePlanAndReviewOwnership(t *testing.T) {
	s, c := newControlled(t)
	ctx := context.Background()
	input := syncRequest(t, s, domain.Delta, 1)
	p, err := s.Preview(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	input.Sets[0].Accounts[0][0] ^= 1
	if _, err = s.Preview(ctx, input); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("changed input: %v", err)
	}
	c.users["external"] = user("external", "outside")
	if _, err = s.Apply(ctx, "one", p.PlanID, p.Revision); !errors.Is(err, domain.ErrStale) || c.adds != 0 {
		t.Fatalf("drift: %v calls=%d", err, c.adds)
	}
}
func TestFullDelegatesVersionedRecovery(t *testing.T) {
	s, c := newControlled(t)
	ctx := context.Background()
	p, err := s.Preview(ctx, syncRequest(t, s, domain.Full, 0))
	if err != nil || c.prepares != 1 {
		t.Fatal(p, err)
	}
	c.fullResult.Ready = false
	r, err := s.Apply(ctx, "one", p.PlanID, p.Revision)
	if err != nil || r.Receipt.Effect != accounts.Partial || !r.Receipt.RequiresRecovery || c.applies != 1 || c.adds != 0 {
		t.Fatal(r, err)
	}
	r, err = s.Recover(ctx, "one", "operation", 8)
	if err != nil || !r.Receipt.Ready || r.Receipt.RequiresRecovery || c.recoveries != 1 {
		t.Fatal(r, err)
	}
}
func TestConcurrentAdmissionStatusAndJoin(t *testing.T) {
	s, c := newControlled(t)
	ctx := context.Background()
	c.entered = make(chan struct{})
	c.unblock = make(chan struct{})
	p, err := s.Preview(ctx, syncRequest(t, s, domain.Delta, 1))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.Apply(ctx, "one", p.PlanID, p.Revision); done <- err }()
	<-c.entered
	status, err := s.Status(ctx, "one", "operation", 0)
	if err != nil || !status.Receipt.InFlight {
		t.Fatal(status, err)
	}
	if _, err = s.Apply(ctx, "one", p.PlanID, p.Revision); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	close(c.unblock)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type blockingObservation struct {
	Observer
	entered chan struct{}
	release chan struct{}
}

func (b blockingObservation) Snapshot(ctx context.Context, id string, tags []string) (domain.Snapshot, error) {
	if len(tags) != 0 {
		b.entered <- struct{}{}
		select {
		case <-ctx.Done():
			return domain.Snapshot{}, ctx.Err()
		case <-b.release:
		}
	}
	return b.Observer.Snapshot(ctx, id, tags)
}
func TestPendingReviewsAreGloballyBoundedBeforeReads(t *testing.T) {
	_, c := newControlled(t)
	observer := blockingObservation{Observer: c, entered: make(chan struct{}, 8), release: make(chan struct{})}
	ids := []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine"}
	s, err := New(ids, observer, c, c, codec.Codec{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 8)
	for _, id := range ids[:8] {
		go func(id string) {
			revision, _ := s.Revision(ctx, id)
			_, err := s.Preview(ctx, domain.Request{InstanceID: id, OperationID: "pending", Expected: revision, Mode: domain.Delta, Selection: []string{"selected"}})
			done <- err
		}(id)
	}
	for range 8 {
		<-observer.entered
	}
	revision, _ := s.Revision(ctx, "nine")
	if _, err := s.Preview(ctx, domain.Request{InstanceID: "nine", OperationID: "ninth", Expected: revision, Mode: domain.Delta, Selection: []string{"selected"}}); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	cancel()
	for range 8 {
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if s.pending != 0 || s.retained != 0 {
		t.Fatalf("pending=%d retained=%d", s.pending, s.retained)
	}
}

func TestExpiredCompletedReceiptsReleaseCapacityWithoutDroppingUncertainty(t *testing.T) {
	_, c := newControlled(t)
	ids := []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine"}
	s, err := New(ids, c, c, c, codec.Codec{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, id := range ids[:8] {
		revision, _ := s.Revision(ctx, id)
		p, err := s.Preview(ctx, domain.Request{InstanceID: id, OperationID: "done", Expected: revision, Mode: domain.Delta, Selection: []string{"selected"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Apply(ctx, id, p.PlanID, p.Revision); err != nil {
			t.Fatal(err)
		}
	}
	s.entries["one"].receipt.RequiresRecovery = true
	advanced := time.Now().Add(domain.Lifetime + time.Second)
	s.now = func() time.Time { return advanced }
	revision, _ := s.Revision(ctx, "nine")
	if _, err := s.Preview(ctx, domain.Request{InstanceID: "nine", OperationID: "next", Expected: revision, Mode: domain.Delta, Selection: []string{"selected"}}); err != nil {
		t.Fatal(err)
	}
	if s.retained != 2 || s.entries["one"].receipt == nil || !s.entries["one"].receipt.RequiresRecovery {
		t.Fatalf("retained=%d uncertain=%+v", s.retained, s.entries["one"].receipt)
	}
}

func TestApplyReceiptOwnsBorrowedInstanceSelector(t *testing.T) {
	s, _ := newControlled(t)
	ctx := context.Background()
	p, err := s.Preview(ctx, syncRequest(t, s, domain.Delta, 1))
	if err != nil {
		t.Fatal(err)
	}
	buffer := []byte("one")
	borrowed := unsafe.String(&buffer[0], len(buffer))
	if _, err := s.Apply(ctx, borrowed, p.PlanID, p.Revision); err != nil {
		t.Fatal(err)
	}
	copy(buffer, "two")
	r, err := s.Status(ctx, "one", "operation", 0)
	if err != nil || r.Receipt.InstanceID != "one" {
		t.Fatal(r, err)
	}
}

type afterAdd struct {
	Accounts
	after func()
}

func (a afterAdd) AddMany(ctx context.Context, id string, items []accounts.Addition, parallel int) (accounts.Batch, error) {
	r, err := a.Accounts.AddMany(ctx, id, items, parallel)
	a.after()
	return r, err
}
func TestFinalAcknowledgementSurvivesCancellationAndFencesConfigPublication(t *testing.T) {
	s, c := newControlled(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.accounts = afterAdd{Accounts: c, after: cancel}
	p, err := s.Preview(ctx, syncRequest(t, s, domain.Delta, 1))
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Apply(ctx, "one", p.PlanID, p.Revision)
	if err != nil || r.Receipt.Effect != accounts.Completed || !r.Receipt.Steps[0].Acknowledged {
		t.Fatal(r, err)
	}
	s, c = newControlled(t)
	ctx = context.Background()
	s.accounts = afterAdd{Accounts: c, after: func() { c.revision++ }}
	p, err = s.Preview(ctx, syncRequest(t, s, domain.Delta, 1))
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.Apply(ctx, "one", p.PlanID, p.Revision)
	if !errors.Is(err, domain.ErrStale) || r.Receipt.Effect != accounts.Unknown || !r.Receipt.RequiresRecovery || !r.Receipt.Steps[0].Acknowledged {
		t.Fatal(r, err)
	}
}
