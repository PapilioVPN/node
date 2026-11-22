package services

import (
	"context"
	"errors"
	"testing"
	"time"

	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
)

func transferRequest() domain.Request {
	return domain.Request{InstanceID: "one", OperationID: "upload", Mode: domain.Full, Selection: []string{"selected"}}
}
func TestChunksInterruptedReplayAndFinalization(t *testing.T) {
	s := NewTransfers()
	ctx := context.Background()
	started, err := s.Begin(ctx, transferRequest(), 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Finish(ctx, "one", started.ID); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unfinished transfer: %v", err)
	}
	c := Chunk{Sequence: 0, Additions: []accounts.Addition{{InboundTag: "selected", Configuration: []byte("abc")}}, Final: true}
	if _, err = s.Append(ctx, "one", started.ID, c); err != nil {
		t.Fatal(err)
	}
	c.Additions[0].Configuration[0] = 'x'
	if _, err = s.Append(ctx, "one", started.ID, c); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("changed replay: %v", err)
	}
	c.Additions[0].Configuration[0] = 'a'
	if status, err := s.Append(ctx, "one", started.ID, c); err != nil || status.NextSequence != 1 {
		t.Fatalf("lost ack replay: %+v %v", status, err)
	}
	r, err := s.Finish(ctx, "one", started.ID)
	if err != nil || string(r.Sets[0].Accounts[0]) != "abc" {
		t.Fatalf("finish: %+v %v", r, err)
	}
	r.Sets[0].Accounts[0][0] = 'z'
	r, _ = s.Finish(ctx, "one", started.ID)
	if string(r.Sets[0].Accounts[0]) != "abc" {
		t.Fatal("finish exposed retained buffer")
	}
	if _, err = s.Append(ctx, "other", started.ID, c); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("scope: %v", err)
	}
	if err = s.Discard(ctx, "one", started.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Finish(ctx, "one", started.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("discard: %v", err)
	}
}
func TestChunksEmptyScopeSequenceAndExpiry(t *testing.T) {
	s := NewTransfers()
	now := time.Now()
	s.now = func() time.Time { return now }
	ctx := context.Background()
	st, err := s.Begin(ctx, transferRequest(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Append(ctx, "one", st.ID, Chunk{Sequence: 1, Final: true}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("gap: %v", err)
	}
	if _, err = s.Append(ctx, "one", st.ID, Chunk{Final: true}); err != nil {
		t.Fatal(err)
	}
	r, err := s.Finish(ctx, "one", st.ID)
	if err != nil || r.Sets[0].Accounts == nil || len(r.Sets[0].Accounts) != 0 {
		t.Fatalf("empty membership: %+v %v", r, err)
	}
	now = now.Add(domain.Lifetime)
	if _, err = s.Finish(ctx, "one", st.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expired: %v", err)
	}
	st, _ = s.Begin(ctx, transferRequest(), 1, 1)
	if _, err = s.Append(ctx, "one", st.ID, Chunk{Additions: []accounts.Addition{{InboundTag: "excluded", Configuration: []byte{1}}}, Final: true}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("excluded: %v", err)
	}
}

func TestChunkBoundsAndGlobalAdmission(t *testing.T) {
	s := NewTransfers()
	ctx := context.Background()
	st, err := s.Begin(ctx, transferRequest(), 51, 51)
	if err != nil {
		t.Fatal(err)
	}
	items := make([]accounts.Addition, 51)
	for i := range items {
		items[i] = accounts.Addition{InboundTag: "selected", Configuration: []byte{1}}
	}
	if _, err := s.Append(ctx, "one", st.ID, Chunk{Additions: items, Final: true}); !errors.Is(err, domain.ErrCapacity) {
		t.Fatalf("51 chunk: %v", err)
	}
	if _, err := s.Append(ctx, "one", st.ID, Chunk{Additions: items[:50]}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(ctx, "one", st.ID, Chunk{Sequence: 1, Additions: items[50:], Final: true}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < domain.MaxTransfers; i++ {
		r := transferRequest()
		r.OperationID += string(rune('a' + i))
		if _, err := s.Begin(ctx, r, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	r := transferRequest()
	r.OperationID = "overflow"
	if _, err := s.Begin(ctx, r, 0, 0); !errors.Is(err, domain.ErrCapacity) {
		t.Fatalf("global: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Finish(cancelled, "one", st.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
