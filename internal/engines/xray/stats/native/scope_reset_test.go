package native

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/traffic"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestScopeResetNativeCallsAndOutcomes(t *testing.T) {
	for _, test := range []struct {
		name  string
		count int
		limit uint32
		lost  int32
		want  error
		swaps int32
	}{
		{name: "empty", limit: 1},
		{name: "one", count: 1, limit: 1, swaps: 1},
		{name: "hundred", count: 100, limit: 100, swaps: 100},
		{name: "thousand", count: 1000, limit: 1000, swaps: 1000},
		{name: "bounded selection", count: 3, limit: 2, want: traffic.ErrTooLarge},
		{name: "lost acknowledgement", count: 4, limit: 4, lost: 2, swaps: 2, want: traffic.ErrResetUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			var queries, swaps atomic.Int32
			counters := []*TrafficCounter{{Name: "inbound>>>edge>>>user>>>outsider>>>traffic>>>uplink", Value: 9}}
			for i := test.count - 1; i >= 0; i-- {
				counters = append(counters, &TrafficCounter{Name: fmt.Sprintf("user>>>team>>>account-%04d>>>traffic>>>uplink", i), Value: 123})
			}
			conn := startResetTestServer(t, func(_ any, stream grpc.ServerStream) error {
				method, _ := grpc.MethodFromServerStream(stream)
				var request ResetCounterRequest
				if err := stream.RecvMsg(&request); err != nil {
					return err
				}
				switch method {
				case queryStats:
					queries.Add(1)
					if request.GetName() != "user>>>" || request.GetReset_() {
						return status.Error(codes.InvalidArgument, "selection must be read-only")
					}
					return stream.SendMsg(&QueryStatsResponse{Stats: counters})
				case resetCounter:
					index := swaps.Add(1)
					want := fmt.Sprintf("user>>>team>>>account-%04d>>>traffic>>>uplink", index-1)
					if request.GetName() != want || !request.GetReset_() {
						return status.Errorf(codes.InvalidArgument, "expected %s", want)
					}
					if index == test.lost {
						return status.Error(codes.Unavailable, "acknowledgement lost")
					}
					return stream.SendMsg(&NamedCounterResponse{Stat: &TrafficCounter{Name: want, Value: -55}})
				default:
					return status.Error(codes.Unimplemented, "unexpected method")
				}
			})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			client := NewTraffic(resetConnection{conn}, false)
			got, err := client.ResetScope(ctx, traffic.ScopeReset{ID: "primary", Scope: traffic.User, MaxCounters: test.limit}, 1)
			if !errors.Is(err, test.want) || queries.Load() != 1 || swaps.Load() != test.swaps {
				t.Fatalf("scope reset error %v, queries %d, swaps %d", err, queries.Load(), swaps.Load())
			}
			if test.want == traffic.ErrTooLarge {
				if got != nil {
					t.Fatalf("over-limit selection returned a report: %+v", got)
				}
				return
			}
			if got == nil || len(got) != test.count {
				t.Fatalf("got %d reported counters, want %d", len(got), test.count)
			}
			for i, row := range got {
				want := traffic.DirectionReset{Effect: traffic.ResetAcknowledged, PreviousBytes: -55}
				if test.lost != 0 && i >= int(test.lost)-1 {
					want = traffic.DirectionReset{Effect: traffic.ResetNotAttempted}
					if i == int(test.lost)-1 {
						want.Effect = traffic.ResetUnknown
					}
				}
				if row.Name != fmt.Sprintf("team>>>account-%04d", i) || row.Direction != traffic.Uplink || row.DirectionReset != want {
					t.Fatalf("counter %d = %+v, want %+v", i, row, want)
				}
			}
		})
	}
}

func TestScopeSelectionRejectsDuplicateBeforeReset(t *testing.T) {
	row := &TrafficCounter{Name: "outbound>>>tag>>>traffic>>>downlink"}
	report, err := selectScopeCounters("outbound>>>", []*TrafficCounter{row, row}, 2)
	if report != nil || !errors.Is(err, traffic.ErrUnavailable) {
		t.Fatalf("duplicate selection = %+v, %v", report, err)
	}
}

func TestConcurrentScopeResetStopsAfterFailure(t *testing.T) {
	counters := []*TrafficCounter{
		{Name: "user>>>alice>>>traffic>>>uplink"},
		{Name: "user>>>bob>>>traffic>>>uplink"},
		{Name: "user>>>charlie>>>traffic>>>uplink"},
	}
	var calls atomic.Int32
	started := make(chan bool)
	conn := startResetTestServer(t, func(_ any, stream grpc.ServerStream) error {
		method, _ := grpc.MethodFromServerStream(stream)
		var request ResetCounterRequest
		if err := stream.RecvMsg(&request); err != nil {
			return err
		}
		if method == queryStats {
			return stream.SendMsg(&QueryStatsResponse{Stats: counters})
		}
		if calls.Add(1) == 2 {
			close(started)
		}
		select {
		case <-started:
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
		if request.GetName() == counters[0].GetName() {
			return status.Error(codes.NotFound, "counter disappeared")
		}
		return status.Error(codes.Unavailable, "acknowledgement lost")
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	client := NewTraffic(resetConnection{conn}, false)
	request := traffic.ScopeReset{ID: "primary", Scope: traffic.User, MaxCounters: 3}
	got, err := client.ResetScope(ctx, request, 2)
	if !errors.Is(err, traffic.ErrResetUnknown) || !errors.Is(err, traffic.ErrNotFound) || calls.Load() != 2 {
		t.Fatalf("concurrent reset called Xray %d times and returned %v", calls.Load(), err)
	}
	if len(got) != 3 || got[0].Effect != traffic.ResetMissing || got[1].Effect != traffic.ResetUnknown || got[2].Effect != traffic.ResetNotAttempted {
		t.Fatalf("concurrent reset report = %+v", got)
	}
}

func TestScopeResetKeepsCancellationWithLocalDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	const name = "user>>>alice>>>traffic>>>uplink"
	deadlines := make(chan bool, 1)
	conn := startResetTestServer(t, func(_ any, stream grpc.ServerStream) error {
		method, _ := grpc.MethodFromServerStream(stream)
		var request ResetCounterRequest
		if err := stream.RecvMsg(&request); err != nil {
			return err
		}
		if method == queryStats {
			return stream.SendMsg(&QueryStatsResponse{Stats: []*TrafficCounter{{Name: name}}})
		}
		_, hasDeadline := stream.Context().Deadline()
		deadlines <- hasDeadline
		cancel()
		<-stream.Context().Done()
		return status.FromContextError(stream.Context().Err()).Err()
	})
	client := NewTraffic(resetConnection{conn}, false)
	request := traffic.ScopeReset{ID: "primary", Scope: traffic.User, MaxCounters: 1}
	got, err := client.ResetScope(ctx, request, 1)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, traffic.ErrResetUnknown) || len(got) != 1 || got[0].Effect != traffic.ResetUnknown {
		t.Fatalf("canceled reset result = %+v, %v", got, err)
	}
	select {
	case hasDeadline := <-deadlines:
		if hasDeadline {
			t.Fatal("scope reset sent the local deadline to Xray")
		}
	default:
		t.Fatal("scope reset never reached the native server")
	}
}
