package api

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestExactCounterUsesOneNonResetGetStatsForAllSixSelections(t *testing.T) {
	for _, scope := range []domain.Scope{domain.User, domain.Inbound, domain.Outbound} {
		for _, direction := range []domain.Direction{domain.Uplink, domain.Downlink} {
			for _, value := range []int64{0, 1<<53 + 1, math.MaxInt64, math.MinInt64} {
				selection := domain.Selection{Scope: scope, Name: " A>>>B/نام ", Direction: direction}
				connection := &connectionFixture{t: t, name: string(scope) + ">>>" + selection.Name + ">>>traffic>>>" + string(direction), value: value}
				got, err := New(connection).Read(context.Background(), selection)
				if err != nil || got != value || connection.calls != 1 {
					t.Fatalf("selection=%+v bytes=%d err=%v GetStats=%d", selection, got, err, connection.calls)
				}
			}
		}
	}
}

func TestNativeFailureAndMalformedRepliesNeverBecomeZeroSuccess(t *testing.T) {
	selection := domain.Selection{Scope: domain.User, Name: "a", Direction: domain.Uplink}
	for _, test := range []struct{ cause, want error }{
		{status.Error(codes.NotFound, "private email"), domain.ErrCounterNotFound},
		{status.Error(codes.Unimplemented, "private endpoint"), domain.ErrUnsupported},
		{status.Error(codes.ResourceExhausted, "private limit"), domain.ErrCapacity},
		{status.Error(codes.Canceled, "canceled"), context.Canceled},
		{status.Error(codes.DeadlineExceeded, "timeout"), context.DeadlineExceeded},
		{rpc.ErrClosed, domain.ErrClosed},
	} {
		connection := &connectionFixture{t: t, name: "user>>>a>>>traffic>>>uplink", err: test.cause}
		_, err := New(connection).Read(context.Background(), selection)
		if !errors.Is(err, test.want) || !errors.Is(err, test.cause) || connection.calls != 1 {
			t.Fatalf("cause=%v err=%v calls=%d", test.cause, err, connection.calls)
		}
	}
	for _, malformed := range []string{"missing", "mismatch"} {
		connection := &connectionFixture{t: t, name: "user>>>a>>>traffic>>>uplink", malformed: malformed}
		if _, err := New(connection).Read(context.Background(), selection); !errors.Is(err, domain.ErrUnavailable) {
			t.Fatalf("%s reply accepted: %v", malformed, err)
		}
	}
	connection := &connectionFixture{t: t}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(connection).Read(ctx, selection); !errors.Is(err, context.Canceled) || connection.calls != 0 {
		t.Fatal("canceled read dispatched", err, connection.calls)
	}
	if _, err := New(connection).Read(context.Background(), domain.Selection{}); !errors.Is(err, domain.ErrInvalidArgument) || connection.calls != 0 {
		t.Fatal("invalid native selection dispatched", err, connection.calls)
	}
}
