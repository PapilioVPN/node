package services

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
)

func TestSameIdentityTwoInstancesAndSeparateExactReads(t *testing.T) {
	counts, lists := 0, 0
	makeReader := func(value int64) reader {
		return reader{
			count: func(ctx context.Context, email string) (domain.Summary, error) {
				counts++
				if email != " A>>>b " {
					t.Fatal(email)
				}
				return domain.Summary{Count: &value}, nil
			},
			ips: func(ctx context.Context, email string) (domain.Visitors, error) {
				lists++
				return domain.Visitors{Tracked: true, IPs: []domain.IP{{Address: "native", LastSeenUnixSeconds: -1}}}, nil
			},
		}
	}
	s, err := New([]Binding{{ID: "a", Reader: makeReader(1)}, {ID: "b", Reader: makeReader(0)}, {ID: "disabled"}}, statusFunc(running))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		result, err := s.Count(context.Background(), domain.Selection{InstanceID: id, Email: " A>>>b "})
		if err != nil || result.InstanceID != id || result.Email != " A>>>b " || result.Generation != 7 || result.ObservedAt.IsZero() || result.Duration < 0 {
			t.Fatal(result, err)
		}
		want := domain.Online
		if id == "b" {
			want = domain.Offline
		}
		if result.State() != want {
			t.Fatal(result)
		}
	}
	result, err := s.IPs(context.Background(), domain.Selection{InstanceID: "a", Email: " A>>>b "})
	if err != nil || len(result.IPs) != 1 || result.IPs[0].LastSeenUnixSeconds != -1 || counts != 2 || lists != 1 {
		t.Fatal(result, err, counts, lists)
	}
	for _, test := range []struct {
		s    domain.Selection
		want error
	}{
		{domain.Selection{InstanceID: "missing", Email: "a"}, domain.ErrNotFound},
		{domain.Selection{InstanceID: "disabled", Email: "a"}, domain.ErrNotConfigured},
		{domain.Selection{InstanceID: "a"}, domain.ErrInvalidArgument},
	} {
		if _, err := s.Count(context.Background(), test.s); !errors.Is(err, test.want) {
			t.Fatal(err)
		}
	}
	if counts != 2 {
		t.Fatal("invalid requests reached native reader")
	}
}

func TestStaleUnavailableCancellationAndNoPartialSuccess(t *testing.T) {
	cause := errors.New("private native failure")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	r := reader{count: func(context.Context, string) (domain.Summary, error) { calls++; return domain.Summary{}, cause }}
	s, _ := New([]Binding{{ID: "a", Reader: r}}, statusFunc(func(context.Context, string) (instances.Status, error) { return instances.Status{}, nil }))
	selection := domain.Selection{InstanceID: "a", Email: "a"}
	if _, err := s.Count(ctx, selection); !errors.Is(err, domain.ErrNotRunning) || calls != 0 {
		t.Fatal(err, calls)
	}
	s.status = statusFunc(running)
	if result, err := s.Count(ctx, selection); !errors.Is(err, cause) || result.InstanceID != "" {
		t.Fatal(result, err)
	}
	statuses := 0
	s.status = statusFunc(func(ctx context.Context, id string) (instances.Status, error) {
		statuses++
		v, _ := running(ctx, id)
		if statuses == 2 {
			v.Execution.Generation++
		}
		return v, nil
	})
	if _, err := s.Count(ctx, selection); !errors.Is(err, domain.ErrExecutionChanged) || !errors.Is(err, cause) {
		t.Fatal(err)
	}
	s.status = statusFunc(running)
	s.entries["a"] = entry{slots: make(chan struct{}, 8), reader: reader{count: func(context.Context, string) (domain.Summary, error) {
		cancel()
		v := int64(1)
		return domain.Summary{Count: &v}, nil
	}}}
	if result, err := s.Count(ctx, selection); !errors.Is(err, context.Canceled) || result.InstanceID != "" {
		t.Fatal(result, err)
	}
}

func TestSharedLocalAndGlobalCapacityAndJoinedCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	entered := make(chan struct{}, 32)
	var calls atomic.Int32
	wait := func(ctx context.Context) { calls.Add(1); entered <- struct{}{}; <-ctx.Done() }
	r := reader{count: func(ctx context.Context, _ string) (domain.Summary, error) {
		wait(ctx)
		return domain.Summary{}, ctx.Err()
	}, ips: func(ctx context.Context, _ string) (domain.Visitors, error) {
		wait(ctx)
		return domain.Visitors{}, ctx.Err()
	}}
	bindings := []Binding{}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		bindings = append(bindings, Binding{ID: id, Reader: r})
	}
	s, _ := New(bindings, statusFunc(running))
	var joined sync.WaitGroup
	for _, id := range []string{"a", "b", "c", "d"} {
		for range 8 {
			joined.Go(func() { s.Count(ctx, domain.Selection{InstanceID: id, Email: "x"}) })
		}
		for range 8 {
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		if _, err := s.IPs(ctx, domain.Selection{InstanceID: id, Email: "x"}); !errors.Is(err, domain.ErrCapacity) {
			t.Fatal("local capacity", err)
		}
	}
	if _, err := s.IPs(ctx, domain.Selection{InstanceID: "e", Email: "x"}); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal("global capacity", err)
	}
	cancel()
	joined.Wait()
	if calls.Load() != 32 || len(s.slots) != 0 {
		t.Fatal(calls.Load(), len(s.slots))
	}
	for _, e := range s.entries {
		if len(e.slots) != 0 {
			t.Fatal("leaked slot")
		}
	}
}
