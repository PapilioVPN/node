package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

func TestPageTraversalUsesOneBulkRead(t *testing.T) {
	for _, n := range []int{0, 1, 50, 51, 1000, 4096} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			records := make([]domain.Record, n)
			for i := range records {
				records[i] = domain.Record{Tag: fmt.Sprintf("tag-%05d", n-i), Alive: true, LastTryUnixSec: 100, HealthPing: nil}
			}
			s, calls := newService(t, records)
			at := time.Unix(101, 0)
			s.now = func() time.Time { return at }
			var all []domain.Status
			token := ""
			var observed time.Time
			for {
				p, err := s.List(context.Background(), "edge", domain.PageRequest{PageToken: token})
				if err != nil || len(p.Statuses) > 50 || p.Total != uint32(n) {
					t.Fatal(p, err)
				}
				if observed.IsZero() {
					observed = p.ObservedAt
				} else if !p.ObservedAt.Equal(observed) {
					t.Fatal("continuation refetched native evidence")
				}
				all = append(all, p.Statuses...)
				token = p.NextPageToken
				if token == "" {
					break
				}
			}
			if len(all) != n || *calls != 1 {
				t.Fatal(len(all), *calls)
			}
			for i := 1; i < len(all); i++ {
				if all[i-1].Tag > all[i].Tag {
					t.Fatal("unstable ordering")
				}
			}
			t.Logf("records=%d pages=%d native RPCs=%d max returned records=50", n, max(1, (n+49)/50), *calls)
		})
	}
}
func TestContinuationFreshnessExpiryReplacementAndIsolation(t *testing.T) {
	s, calls := newService(t, []domain.Record{{Tag: "a", Alive: true, LastTryUnixSec: 100}, {Tag: "b", Alive: true, LastTryUnixSec: 100}})
	at := time.Unix(101, 0)
	s.now = func() time.Time { return at }
	p, err := s.List(context.Background(), "edge", domain.PageRequest{PageSize: 1, MaxSampleAgeSeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	original := p.NextPageToken
	at = at.Add(2 * time.Second)
	next, err := s.List(context.Background(), "edge", domain.PageRequest{PageToken: original, MaxSampleAgeSeconds: 1})
	if err != nil || next.Statuses[0].Freshness != "stale" || !next.ObservedAt.Equal(p.ObservedAt) || !next.ServedAt.Equal(at) || *calls != 1 {
		t.Fatal(next, err, *calls)
	}
	next.Statuses[0].Tag = "mutated"
	again, err := s.List(context.Background(), "edge", domain.PageRequest{PageToken: original, MaxSampleAgeSeconds: 1})
	if err != nil || again.Statuses[0].Tag != "b" {
		t.Fatal(again, err)
	}
	if _, err := s.List(context.Background(), "edge", domain.PageRequest{PageToken: original, MaxSampleAgeSeconds: 2}); !errors.Is(err, domain.ErrContinuation) {
		t.Fatal(err)
	}
	s.entries["other"] = s.entries["edge"]
	if _, err := s.List(context.Background(), "other", domain.PageRequest{PageToken: original, MaxSampleAgeSeconds: 1}); !errors.Is(err, domain.ErrContinuation) {
		t.Fatal(err)
	}
	at = at.Add(30 * time.Second)
	if _, err := s.List(context.Background(), "edge", domain.PageRequest{PageToken: original, MaxSampleAgeSeconds: 1}); !errors.Is(err, domain.ErrContinuation) {
		t.Fatal(err)
	}
	p, _ = s.List(context.Background(), "edge", domain.PageRequest{PageSize: 1})
	_, _ = s.List(context.Background(), "edge", domain.PageRequest{PageSize: 1})
	if _, err := s.List(context.Background(), "edge", domain.PageRequest{PageToken: p.NextPageToken}); !errors.Is(err, domain.ErrContinuation) {
		t.Fatal(err)
	}
	p, _ = s.List(context.Background(), "edge", domain.PageRequest{PageSize: 1})
	s.status = statusFunc(func(context.Context, string) (instances.Status, error) {
		return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: 8}}, nil
	})
	if _, err := s.List(context.Background(), "edge", domain.PageRequest{PageToken: p.NextPageToken}); !errors.Is(err, domain.ErrContinuation) {
		t.Fatal(err)
	}
	fresh, _ := s.List(context.Background(), "edge", domain.PageRequest{})
	if fresh.Generation != 8 {
		t.Fatal(fresh)
	}
}
func TestCollectionBoundsAndDuplicateOrder(t *testing.T) {
	s, _ := newService(t, []domain.Record{{Tag: "dup", DelayMS: 1}, {Tag: "dup", DelayMS: 2}, {Tag: ""}})
	got, err := s.List(context.Background(), "edge", domain.PageRequest{})
	if err != nil || len(got.Statuses) != 3 || got.Statuses[0].Tag != "" || got.Statuses[1].DelayMS != 1 || got.Statuses[2].DelayMS != 2 {
		t.Fatal(got, err)
	}
	for _, request := range []domain.PageRequest{{PageSize: 51}, {PageToken: "bad"}, {PageToken: strings.Repeat("x", 97)}, {MaxSampleAgeSeconds: 86401}} {
		if _, err := s.List(context.Background(), "edge", request); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatal(err)
		}
	}
	tooMany := make([]domain.Record, 4097)
	s, _ = newService(t, tooMany)
	if _, err := s.List(context.Background(), "edge", domain.PageRequest{}); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	// Independent retention capacity, including cleanup without native re-fetch.
	s, _ = newService(t, []domain.Record{{Tag: "a"}, {Tag: "b"}})
	for i := 0; i < 9; i++ {
		id := fmt.Sprintf("e%d", i)
		s.entries[id] = s.entries["edge"]
		_, err := s.List(context.Background(), id, domain.PageRequest{PageSize: 1})
		if i < 8 && err != nil {
			t.Fatal(err)
		}
		if i == 8 && !errors.Is(err, domain.ErrCapacity) {
			t.Fatal(err)
		}
	}
}

func TestRetentionByteBudgetAndPointerOwnership(t *testing.T) {
	records := []domain.Record{{Tag: "a", HealthPing: &domain.HealthPing{All: 2}}, {Tag: "b", HealthPing: &domain.HealthPing{All: 3}}}
	s, _ := newService(t, records)
	p, err := s.List(context.Background(), "edge", domain.PageRequest{PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	*p.ExpiresAt = time.Time{}
	p.Statuses[0].HealthPing.All = 100
	saved, err := s.List(context.Background(), "edge", domain.PageRequest{PageToken: p.NextPageToken, PageSize: 1})
	if err != nil || saved.ExpiresAt.IsZero() || saved.Statuses[0].HealthPing.All != 3 {
		t.Fatal(saved, err)
	}
	saved.Statuses[0].HealthPing.All = 100
	saved, err = s.List(context.Background(), "edge", domain.PageRequest{PageToken: p.NextPageToken, PageSize: 1})
	if err != nil || saved.Statuses[0].HealthPing.All != 3 {
		t.Fatal(saved, err)
	}
	oversized := make([]domain.Record, 4096)
	for i := range oversized {
		oversized[i].Tag = strings.Repeat("x", 4096)
	}
	s, _ = newService(t, oversized)
	if _, err := s.List(context.Background(), "edge", domain.PageRequest{}); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	// Failed capacity admission must preserve the previous valid continuation.
	s, _ = newService(t, records)
	p, _ = s.List(context.Background(), "edge", domain.PageRequest{PageSize: 1})
	for key, old := range s.snapshots {
		old.bytes = maxSnapshotBytes
		s.snapshots[key] = old
	}
	s.entries["other"] = s.entries["edge"]
	if _, err := s.List(context.Background(), "other", domain.PageRequest{PageSize: 1}); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	if _, err := s.List(context.Background(), "edge", domain.PageRequest{PageToken: p.NextPageToken}); err != nil {
		t.Fatal(err)
	}
}
