package services

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type listFunc func(context.Context, domain.Scope) ([]domain.Counter, error)

func (f listFunc) List(c context.Context, s domain.Scope) ([]domain.Counter, error) { return f(c, s) }

type resetFunc func(context.Context, domain.Selection) (int64, error)

func (f resetFunc) Reset(c context.Context, s domain.Selection) (int64, error) { return f(c, s) }
func records(n int) []domain.Counter {
	r := make([]domain.Counter, n)
	for i := range r {
		r[i] = domain.Counter{Selection: domain.Selection{Scope: domain.User, Name: fmt.Sprintf("user-%05d", i), Direction: domain.Uplink}, Bytes: int64(i)}
	}
	return r
}
func TestOneScanStablePagesAndBudgets(t *testing.T) {
	for _, n := range []int{0, 1, 50, 51, 1000, 65536} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			calls := 0
			source := records(n)
			s, _ := New([]Binding{{ID: "a", Lister: listFunc(func(context.Context, domain.Scope) ([]domain.Counter, error) { calls++; return source, nil })}}, statusFunc(running))
			request := domain.PageRequest{Scope: domain.User}
			var all []domain.Counter
			var timestamp time.Time
			for {
				page, err := s.List(context.Background(), "a", request)
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Counters) > 50 || page.Generation != 7 {
					t.Fatal(page)
				}
				if timestamp.IsZero() {
					timestamp = page.ObservedAt
				}
				if page.ObservedAt != timestamp {
					t.Fatal("fresh scan per page")
				}
				all = append(all, page.Counters...)
				request.PageToken = page.NextPageToken
				if request.PageToken == "" {
					break
				}
			}
			if calls != 1 || len(all) != n || (n > 0 && !reflect.DeepEqual(all, source)) {
				t.Fatalf("calls=%d items=%d", calls, len(all))
			}
		})
	}
}
func TestContinuationScopeInstanceExpiryAndReplacement(t *testing.T) {
	generation := uint64(1)
	s, _ := New([]Binding{{ID: "a", Lister: listFunc(func(context.Context, domain.Scope) ([]domain.Counter, error) { return records(51), nil })}, {ID: "b"}}, statusFunc(func(context.Context, string) (instances.Status, error) {
		return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: generation}}, nil
	}))
	first, _ := s.List(context.Background(), "a", domain.PageRequest{Scope: domain.User})
	for _, r := range []struct {
		id    string
		scope domain.Scope
	}{{"a", domain.Inbound}, {"b", domain.User}} {
		if _, err := s.List(context.Background(), r.id, domain.PageRequest{Scope: r.scope, PageToken: first.NextPageToken}); !errors.Is(err, domain.ErrContinuation) {
			t.Fatal(err)
		}
	}
	generation++
	if _, err := s.List(context.Background(), "a", domain.PageRequest{Scope: domain.User, PageToken: first.NextPageToken}); !errors.Is(err, domain.ErrContinuation) {
		t.Fatal(err)
	}
	first, _ = s.List(context.Background(), "a", domain.PageRequest{Scope: domain.User})
	for key, v := range s.snapshots {
		v.expires = time.Now().Add(-time.Second)
		s.snapshots[key] = v
	}
	if _, err := s.List(context.Background(), "a", domain.PageRequest{Scope: domain.User, PageToken: first.NextPageToken}); !errors.Is(err, domain.ErrContinuation) {
		t.Fatal(err)
	}
	if _, err := s.List(context.Background(), "a", domain.PageRequest{Scope: domain.User, PageSize: 51}); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatal(err)
	}
}
func TestPairsKeepMissingAndZeroWithTwoCalls(t *testing.T) {
	for _, mask := range []int{0, 1, 2, 3} {
		calls := 0
		s, _ := New([]Binding{{ID: "a", Reader: readerFunc(func(_ context.Context, v domain.Selection) (int64, error) {
			calls++
			bit := 1
			if v.Direction == domain.Downlink {
				bit = 2
			}
			if mask&bit == 0 {
				return 0, domain.ErrCounterNotFound
			}
			return 0, nil
		})}}, statusFunc(running))
		result, err := s.ReadPair(context.Background(), "a", domain.User, "x")
		if err != nil || calls != 2 || (result.Uplink != nil) != (mask&1 != 0) || (result.Downlink != nil) != (mask&2 != 0) {
			t.Fatal(result, err, calls)
		}
	}
}
