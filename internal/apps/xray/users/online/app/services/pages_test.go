package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"

	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
)

func TestPagesRetainOneNativeObservation(t *testing.T) {
	for _, size := range []int{0, 1, 50, 51, 1000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			r := &collectionReader{size: size}
			s, _ := New([]Binding{{ID: "a", Reader: reader{}, Collection: r}}, statusFunc(running))
			request := domain.PageRequest{Selection: domain.Selection{InstanceID: "a"}}
			total := 0
			var first domain.Page
			for {
				p, err := s.List(context.Background(), request)
				if err != nil {
					t.Fatal(err)
				}
				if total == 0 {
					first = p
				}
				if len(p.Records) > 50 || p.Total != size || p.Generation != first.Generation || !p.ObservedAt.Equal(first.ObservedAt) {
					t.Fatal(p)
				}
				total += len(p.Records)
				request.PageToken = p.NextPageToken
				if request.PageToken == "" {
					break
				}
			}
			if total != size || r.calls != 1 {
				t.Fatal(total, r.calls)
			}
		})
	}
}

func TestContinuationScopeExpiryRestartAndOwnership(t *testing.T) {
	r := &collectionReader{size: 51}
	generation := uint64(7)
	state := instances.Running
	s, _ := New([]Binding{{ID: "a", Reader: reader{}, Collection: r}, {ID: "b", Reader: reader{}, Collection: r}}, statusFunc(func(context.Context, string) (instances.Status, error) {
		return instances.Status{Execution: instances.Execution{State: state, Generation: generation}}, nil
	}))
	request := domain.PageRequest{Selection: domain.Selection{InstanceID: "a"}}
	first, err := s.List(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	first.Records[0].Email = "corrupted"
	request.PageToken = first.NextPageToken
	for _, bad := range []domain.PageRequest{
		{Selection: domain.Selection{InstanceID: "b"}, PageToken: request.PageToken},
		{Selection: domain.Selection{InstanceID: "a"}, PageToken: request.PageToken, PageSize: 1},
		{Selection: domain.Selection{InstanceID: "a"}, PageToken: first.ObservationID + ":1"},
	} {
		if _, err := s.List(context.Background(), bad); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatal(err)
		}
	}
	if _, err := s.Records(context.Background(), request); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatal(err)
	}
	generation++
	if _, err := s.List(context.Background(), request); !errors.Is(err, domain.ErrExecutionChanged) {
		t.Fatal(err)
	}
	generation--
	state = instances.Stopped
	if _, err := s.List(context.Background(), request); !errors.Is(err, domain.ErrNotRunning) {
		t.Fatal(err)
	}
	state = instances.Running
	s.pages.observations[first.ObservationID].page.ExpiresAt = time.Now().Add(-time.Second)
	if _, err := s.List(context.Background(), request); !errors.Is(err, domain.ErrObservationExpired) {
		t.Fatal(err)
	}
	if r.calls != 1 {
		t.Fatal("continuation repeated full read", r.calls)
	}
	fresh, _ := New([]Binding{{ID: "a", Reader: reader{}, Collection: r}}, statusFunc(running))
	if _, err := fresh.List(context.Background(), request); !errors.Is(err, domain.ErrObservationExpired) {
		t.Fatal("Node restart", err)
	}
}

func TestSlowReadersHaveBoundedRetentionAndNoEviction(t *testing.T) {
	r := &collectionReader{size: 51}
	s, _ := New([]Binding{{ID: "a", Reader: reader{}, Collection: r}}, statusFunc(running))
	request := domain.PageRequest{Selection: domain.Selection{InstanceID: "a"}}
	var first domain.Page
	for i := range domain.MaxObservations {
		p, err := s.List(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = p
		}
	}
	if _, err := s.List(context.Background(), request); !errors.Is(err, domain.ErrCapacity) || r.calls != domain.MaxObservations {
		t.Fatal(err, r.calls)
	}
	request.PageToken = first.NextPageToken
	if p, err := s.List(context.Background(), request); err != nil || p.Fresh || p.ObservationID != first.ObservationID || len(p.Records) != 1 {
		t.Fatal(p, err)
	}
	// Expired observations release space on the next access, without background work.
	for _, v := range s.pages.observations {
		v.page.ExpiresAt = time.Now().Add(-time.Second)
	}
	request.PageToken = ""
	if _, err := s.List(context.Background(), request); err != nil {
		t.Fatal(err)
	}
}

func TestPagedExactIPsAndLegacyBound(t *testing.T) {
	calls := 0
	r := reader{ips: func(context.Context, string) (domain.Visitors, error) {
		calls++
		ips := make([]domain.IP, 51)
		for i := range ips {
			ips[i] = domain.IP{Address: fmt.Sprint(i), LastSeenUnixSeconds: -1}
		}
		return domain.Visitors{Tracked: true, IPs: ips}, nil
	}}
	s, _ := New([]Binding{{ID: "a", Reader: r}}, statusFunc(running))
	req := domain.PageRequest{Selection: domain.Selection{InstanceID: "a", Email: "opaque>>>email"}}
	first, err := s.PageIPs(context.Background(), req)
	if err != nil || len(first.Records) != 50 || first.State != domain.Online {
		t.Fatal(first, err)
	}
	first.Records[0].IP.Address = "corrupted"
	req.PageToken = first.NextPageToken
	second, err := s.PageIPs(context.Background(), req)
	if err != nil || len(second.Records) != 1 || calls != 1 || second.State != domain.Online {
		t.Fatal(second, err, calls)
	}
	if _, err := s.IPs(context.Background(), req.Selection); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
}

func TestFirstPageBoundsCancellationAndGeneration(t *testing.T) {
	for _, rows := range [][]domain.Record{make([]domain.Record, domain.MaxRecords+1), {{Email: strings.Repeat("x", domain.MaxObservationBytes+1)}}} {
		r := collectionFunc(func(context.Context) ([]domain.Record, error) { return rows, nil })
		s, _ := New([]Binding{{ID: "a", Reader: reader{}, Collection: r}}, statusFunc(running))
		if p, err := s.List(context.Background(), domain.PageRequest{Selection: domain.Selection{InstanceID: "a"}}); !errors.Is(err, domain.ErrCapacity) || len(p.Records) != 0 || s.pages.pending != 0 || len(s.pages.observations) != 0 {
			t.Fatal(p, err)
		}
	}
	for _, changed := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		r := collectionFunc(func(context.Context) ([]domain.Record, error) {
			if !changed {
				cancel()
			}
			return make([]domain.Record, 51), nil
		})
		s, _ := New([]Binding{{ID: "a", Reader: reader{}, Collection: r}}, statusFunc(func(ctx context.Context, id string) (instances.Status, error) {
			status, _ := running(ctx, id)
			calls++
			if calls == 2 && changed {
				status.Execution.Generation++
			}
			return status, nil
		}))
		p, err := s.List(ctx, domain.PageRequest{Selection: domain.Selection{InstanceID: "a"}})
		want := context.Canceled
		if changed {
			want = domain.ErrExecutionChanged
		}
		if !errors.Is(err, want) || len(p.Records) != 0 || len(s.pages.observations) != 0 || s.pages.pending != 0 {
			t.Fatal(p, err)
		}
		cancel()
	}
}
func TestPendingObservationsBoundNativeWorkAndJoin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{}, domain.MaxObservations)
	r := collectionFunc(func(ctx context.Context) ([]domain.Record, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	s, _ := New([]Binding{{ID: "a", Reader: reader{}, Collection: r}}, statusFunc(running))
	done := make(chan error, domain.MaxObservations)
	for range domain.MaxObservations {
		go func() {
			_, err := s.List(ctx, domain.PageRequest{Selection: domain.Selection{InstanceID: "a"}})
			done <- err
		}()
	}
	for range domain.MaxObservations {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("native work did not enter")
		}
	}
	if _, err := s.List(ctx, domain.PageRequest{Selection: domain.Selection{InstanceID: "a"}}); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	cancel()
	for range domain.MaxObservations {
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("native work did not join")
		}
	}
	if s.pages.pending != 0 || len(s.slots) != 0 || len(s.pages.observations) != 0 {
		t.Fatal("leaked read capacity")
	}
}
