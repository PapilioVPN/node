package services

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

func intent(s *Service, kind domain.ResetKind) domain.ResetRequest {
	r := domain.ResetRequest{Epoch: s.epoch, OperationID: "operation-1", Kind: kind, Selection: exact}
	if kind == domain.ResetPair {
		r.Selection.Direction = ""
	}
	if kind == domain.ResetScope {
		r.Selection.Name = ""
		r.Selection.Direction = ""
		r.MaxCounters = 1000
	}
	return r
}
func TestResetSizesCallCountsAndReadbackPages(t *testing.T) {
	for _, n := range []int{0, 1, 50, 51, 1000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			scans, resets := 0, 0
			s, _ := New([]Binding{{ID: "a", Lister: listFunc(func(context.Context, domain.Scope) ([]domain.Counter, error) { scans++; return records(n), nil }), Resetter: resetFunc(func(context.Context, domain.Selection) (int64, error) { resets++; return -123, nil })}}, statusFunc(running))
			request := intent(s, domain.ResetScope)
			result, err := s.Reset(context.Background(), "a", request)
			if err != nil || result.Outcome != "completed" || scans != 1 || resets != n || result.Total != uint32(n) {
				t.Fatal(result, err, scans, resets)
			}
			count := len(result.Items)
			for result.NextOffset > 0 {
				result, err = s.ResetStatus(context.Background(), "a", result.Revision, result.NextOffset)
				if err != nil || len(result.Items) > 50 {
					t.Fatal(err, result)
				}
				count += len(result.Items)
			}
			if count != n {
				t.Fatal(count)
			}
			again, err := s.Reset(context.Background(), "a", request)
			if err != nil || again.OperationID != request.OperationID || resets != n || scans != 1 {
				t.Fatal("replay performed effects", again, err)
			}
		})
	}
}
func TestResetPartialUnknownFencingAndRevisionCAS(t *testing.T) {
	calls := 0
	generation := uint64(7)
	s, _ := New([]Binding{{ID: "a", Lister: listFunc(func(context.Context, domain.Scope) ([]domain.Counter, error) { return records(4), nil }), Resetter: resetFunc(func(context.Context, domain.Selection) (int64, error) {
		calls++
		if calls == 2 {
			return 0, errors.Join(domain.ErrResetUnknown, context.DeadlineExceeded)
		}
		return 0, nil
	})}}, statusFunc(func(context.Context, string) (instances.Status, error) {
		return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: generation}}, nil
	}))
	r := intent(s, domain.ResetScope)
	result, err := s.Reset(context.Background(), "a", r)
	if err != nil || result.Outcome != "unknown" || !result.RecoveryRequired || calls != 2 || result.Items[0].Effect != domain.Acknowledged || *result.Items[0].PreviousBytes != 0 || result.Items[1].Effect != domain.Unknown || result.Items[2].Attempted {
		t.Fatal(result, err)
	}
	result.Items[0].Effect = domain.Unknown
	*result.Items[0].PreviousBytes = 12
	status, _ := s.ResetStatus(context.Background(), "a", 1, 0)
	if status.Items[0].Effect != domain.Acknowledged || *status.Items[0].PreviousBytes != 0 {
		t.Fatal("caller corrupted status")
	}
	r.OperationID = "operation-2"
	r.ExpectedRevision = 1
	if _, err := s.Reset(context.Background(), "a", r); !errors.Is(err, domain.ErrRestartRequired) {
		t.Fatal(err)
	}
	generation++
	if result, err = s.Reset(context.Background(), "a", r); err != nil || result.Outcome != "completed" {
		t.Fatal(result, err)
	}
	old := intent(s, domain.ResetScope)
	if _, err := s.Reset(context.Background(), "a", old); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("old replay accepted", err)
	}
	r.Epoch = "00000000000000000000000000000000"
	r.OperationID = "restart-replay"
	r.ExpectedRevision = 2
	if _, err := s.Reset(context.Background(), "a", r); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("old Node epoch accepted", err)
	}
}
func TestResetPairMissingAndKnownPartial(t *testing.T) {
	for _, failure := range []error{domain.ErrCounterNotFound, domain.ErrUnsupported} {
		calls := 0
		s, _ := New([]Binding{{ID: "a", Resetter: resetFunc(func(context.Context, domain.Selection) (int64, error) {
			calls++
			if calls == 1 {
				return 0, failure
			}
			return 0, nil
		})}}, statusFunc(running))
		result, err := s.Reset(context.Background(), "a", intent(s, domain.ResetPair))
		if err != nil {
			t.Fatal(err)
		}
		if errors.Is(failure, domain.ErrCounterNotFound) {
			if calls != 2 || result.Items[0].Effect != domain.Missing || result.Items[1].Effect != domain.Acknowledged {
				t.Fatal(result)
			}
		} else if calls != 1 || result.Items[1].Effect != domain.NotAttempted {
			t.Fatal(result)
		}
	}
	calls := 0
	s, _ := New([]Binding{{ID: "a", Resetter: resetFunc(func(context.Context, domain.Selection) (int64, error) {
		calls++
		if calls == 2 {
			return 0, domain.ErrUnsupported
		}
		return 1, nil
	})}}, statusFunc(running))
	result, _ := s.Reset(context.Background(), "a", intent(s, domain.ResetPair))
	if result.Outcome != "partial" || result.Items[0].Effect != domain.Acknowledged || result.Items[1].Effect != domain.NotApplied {
		t.Fatal(result)
	}
}
func TestResetCancellationLateAckAndInFlightReadback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	s, _ := New([]Binding{{ID: "a", Resetter: resetFunc(func(context.Context, domain.Selection) (int64, error) {
		close(entered)
		<-release
		cancel()
		return 0, nil
	})}}, statusFunc(running))
	request := intent(s, domain.ResetExact)
	var group sync.WaitGroup
	group.Go(func() {
		result, err := s.Reset(ctx, "a", request)
		if err != nil || result.Outcome != "completed" || result.Items[0].Effect != domain.Acknowledged {
			t.Error(result, err)
		}
	})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("not entered")
	}
	status, err := s.ResetStatus(context.Background(), "a", 0, 0)
	if err != nil || status.Outcome != "in_progress" || status.Items[0].Effect != domain.Unknown || !status.Items[0].Attempted {
		t.Fatal(status, err)
	}
	duplicate, err := s.Reset(context.Background(), "a", request)
	if err != nil || duplicate.Outcome != "in_progress" {
		t.Fatal(duplicate, err)
	}
	request.OperationID = "conflict"
	if _, err := s.Reset(context.Background(), "a", request); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	close(release)
	group.Wait()
}
func TestScopeCapacityAndDuplicatesHaveNoEffects(t *testing.T) {
	for _, source := range [][]domain.Counter{records(1001), {records(1)[0], records(1)[0]}} {
		s, _ := New([]Binding{{ID: "a", Lister: listFunc(func(context.Context, domain.Scope) ([]domain.Counter, error) { return source, nil }), Resetter: resetFunc(func(context.Context, domain.Selection) (int64, error) { t.Error("unexpected reset"); return 0, nil })}}, statusFunc(running))
		r, err := s.Reset(context.Background(), "a", intent(s, domain.ResetScope))
		if err != nil || r.Outcome != "not_applied" || r.Error == "" {
			t.Fatal(r, err)
		}
	}
}
