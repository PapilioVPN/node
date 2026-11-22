package services_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
)

func TestInvalidReplacementDoesNotPrepareOrDispatch(t *testing.T) {
	s, err := services.New(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []domain.AddRequest{
		{InstanceID: "one", RuleJSON: []byte(`{}`)},
		{InstanceID: "one", RuleJSON: []byte(`{}`), Mode: domain.Replace},
	} {
		result, err := s.Add(context.Background(), request)
		if !errors.Is(err, domain.ErrInvalidArgument) || result.Effect != domain.NotApplied {
			t.Fatalf("result=%+v error=%v", result, err)
		}
	}
}

func TestRulePagesReadOnceAndPreservePriority(t *testing.T) {
	for _, n := range []int{0, 1, 50, 51, 1000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			b := &backend{rows: make([]domain.Rule, n)}
			for i := range b.rows {
				b.rows[i] = domain.Rule{RuleTag: fmt.Sprint(i), OutboundTag: "same"}
			}
			s, state := setup(b, nil)
			ctx := context.Background()
			request := domain.PageRequest{InstanceID: "one"}
			seen := 0
			observation := ""
			for {
				page, err := s.List(ctx, request)
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Rules) > 50 || page.Total != n || page.Generation != 1 {
					t.Fatalf("invalid page %+v", page)
				}
				if observation != "" && (observation != page.ObservationID || page.Fresh) {
					t.Fatal("continuation changed observation")
				}
				observation = page.ObservationID
				for _, row := range page.Rules {
					if row.RuleTag != fmt.Sprint(seen) {
						t.Fatal("priority changed", row, seen)
					}
					seen++
				}
				if page.NextPageToken == "" {
					break
				}
				request.PageToken = page.NextPageToken
			}
			if seen != n || b.lists != 1 {
				t.Fatal("native call growth", seen, b.lists)
			}
			if n > 50 {
				state.generation.Store(2)
				_, err := s.List(ctx, request)
				if !errors.Is(err, domain.ErrExecutionChanged) {
					t.Fatal("stale continuation accepted", err)
				}
			}
		})
	}
}
func TestUncertainEffectsFenceAndExplicitExecutionReplacementRecovers(t *testing.T) {
	b := &backend{invoke: func() (domain.NativeResult, error) {
		return domain.NativeResult{Effect: domain.Unknown, RequiresInstanceRestart: true}, context.DeadlineExceeded
	}}
	s, state := setup(b, nil)
	ctx := context.Background()
	o, err := s.Remove(ctx, "one", "tag")
	if !errors.Is(err, context.DeadlineExceeded) || o.Effect != domain.Unknown || !o.RequiresInstanceRestart {
		t.Fatalf("%+v %v", o, err)
	}
	if _, err := s.List(ctx, domain.PageRequest{InstanceID: "one"}); !errors.Is(err, domain.ErrUncertain) {
		t.Fatal(err)
	}
	if _, err := s.Remove(ctx, "one", "tag"); !errors.Is(err, domain.ErrUncertain) || b.writes != 1 {
		t.Fatal("ambiguous operation replayed", err)
	}
	status, err := s.Status(ctx, "one")
	if err != nil || !status.RequiresInstanceRestart || status.LastOperation.Effect != domain.Unknown {
		t.Fatalf("%+v %v", status, err)
	}
	state.generation.Store(2)
	b.invoke = nil
	o, err = s.Remove(ctx, "one", "tag")
	if err != nil || o.Generation != 2 || o.Effect != domain.Completed {
		t.Fatalf("%+v %v", o, err)
	}
}
func TestPreparationFailureAndGenerationChangeSubmitNothing(t *testing.T) {
	b := &backend{}
	s, state := setup(b, nil)
	calls := 0
	s2, err := services.New([]services.Binding{{ID: "one", Backend: b}}, state, prepareFunc(func(context.Context, string, []byte) ([]byte, error) {
		calls++
		state.generation.Store(2)
		return []byte("compiled"), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	o, err := s2.Add(context.Background(), domain.AddRequest{InstanceID: "one", RuleJSON: []byte(`{}`), Mode: domain.Append})
	if !errors.Is(err, domain.ErrExecutionChanged) || o.Effect != domain.NotApplied || b.writes != 0 || calls != 1 {
		t.Fatalf("%+v %v calls=%d", o, err, calls)
	}
	_, err = s.Add(context.Background(), domain.AddRequest{InstanceID: "one", RuleJSON: []byte(`{}`), Mode: domain.Append})
	if !errors.Is(err, domain.ErrUnsupported) || b.writes != 0 {
		t.Fatal(err)
	}
}
func TestLateAcknowledgementAndAdmission(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	b := &backend{invoke: func() (domain.NativeResult, error) {
		cancel()
		return domain.NativeResult{Effect: domain.Completed, Acknowledged: true}, nil
	}}
	s, _ := setup(b, nil)
	o, err := s.Remove(ctx, "one", "tag")
	if err != nil || !o.Acknowledged || o.Effect != domain.Completed {
		t.Fatalf("ack lost %+v %v", o, err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	b.invoke = func() (domain.NativeResult, error) {
		close(entered)
		<-release
		return domain.NativeResult{Effect: domain.Completed, Acknowledged: true}, nil
	}
	go func() { defer close(done); _, _ = s.Remove(context.Background(), "one", "tag") }()
	<-entered
	if _, err := s.Remove(context.Background(), "one", "tag"); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	if _, err := s.Balancer(context.Background(), "one", "tag"); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	status, err := s.Status(context.Background(), "one")
	if err != nil || !status.RequestInFlight {
		t.Fatal("status blocked", err)
	}
	close(release)
	<-done
}
func TestOverrideRequiresTargetPresenceAndProbeIsSelectionOnly(t *testing.T) {
	b := &backend{}
	s, _ := setup(b, nil)
	if o, err := s.Override(context.Background(), domain.OverrideRequest{InstanceID: "one", BalancerTag: "b"}); !errors.Is(err, domain.ErrInvalidArgument) || o.Effect != domain.NotApplied || b.writes != 0 {
		t.Fatal(o, err)
	}
	clear := ""
	if _, err := s.Override(context.Background(), domain.OverrideRequest{InstanceID: "one", BalancerTag: "b", Target: &clear}); err != nil {
		t.Fatal(err)
	}
	p, err := s.Probe(context.Background(), domain.ProbeRequest{InstanceID: "one"})
	if err != nil || p.Route.OutboundTag != "selected" || !p.Operation.Acknowledged {
		t.Fatal(p, err)
	}
}
