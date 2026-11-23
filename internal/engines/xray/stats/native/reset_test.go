package native

import (
	"context"
	"errors"
	"math"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/traffic"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type resetConnection struct{ conn *grpc.ClientConn }

func (owner resetConnection) NativeConnection(string) (*grpc.ClientConn, error) {
	return owner.conn, nil
}

func TestResetReportsLostAcknowledgementWithoutRepeating(t *testing.T) {
	const key = "user>>>team>>>alice>>>traffic>>>uplink"
	var calls atomic.Int32
	var bytes atomic.Int64
	bytes.Store(math.MaxInt64)
	conn := startResetTestServer(t, func(_ any, stream grpc.ServerStream) error {
		var request ResetCounterRequest
		if err := stream.RecvMsg(&request); err != nil {
			return err
		}
		method, _ := grpc.MethodFromServerStream(stream)
		if request.Name != key || !request.GetReset_() || method != resetCounter {
			return status.Error(codes.InvalidArgument, "invalid exact native request")
		}
		previous := bytes.Swap(0)
		if calls.Add(1) == 1 {
			return status.Error(codes.Unavailable, "acknowledgement lost")
		}
		return stream.SendMsg(&NamedCounterResponse{Stat: &TrafficCounter{Name: key, Value: previous}})
	})
	client := NewTraffic(resetConnection{conn}, false)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	counter := traffic.Counter{ID: "primary", Scope: traffic.User, Name: "team>>>alice", Direction: traffic.Uplink}
	got, err := client.Reset(ctx, counter)
	if got != 0 || !errors.Is(err, traffic.ErrResetUnknown) || status.Code(err) != codes.Unavailable ||
		calls.Load() != 1 || bytes.Load() != 0 {
		t.Fatalf("lost acknowledgement = (%d, %v), calls %d, bytes %d", got, err, calls.Load(), bytes.Load())
	}

	bytes.Store(math.MaxInt64)
	got, err = client.Reset(ctx, counter)
	if got != math.MaxInt64 || err != nil || calls.Load() != 2 || bytes.Load() != 0 {
		t.Fatalf("acknowledgement = (%d, %v), calls %d, bytes %d", got, err, calls.Load(), bytes.Load())
	}
}

func startResetTestServer(t *testing.T, handler grpc.StreamHandler) *grpc.ClientConn {
	t.Helper()
	listener := bufconn.Listen(64 << 10)
	server := grpc.NewServer(grpc.UnknownServiceHandler(handler))
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
		<-done
	})
	conn, err := grpc.NewClient("passthrough:///reset-fixture", grpc.WithDisableRetry(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

type replacingExecution struct {
	conn     *grpc.ClientConn
	replaced *atomic.Bool
}

func (owner replacingExecution) NativeConnection(string) (*grpc.ClientConn, error) {
	if owner.replaced.Load() {
		return nil, traffic.ErrUnavailable
	}
	return owner.conn, nil
}

func TestResetPairKeepsAcknowledgedProgress(t *testing.T) {
	for _, test := range []struct {
		name     string
		first    codes.Code
		second   codes.Code
		replaced bool
		want     traffic.PairReset
		err      error
		calls    int32
	}{
		{
			name: "second reply lost", second: codes.Unavailable,
			want: traffic.PairReset{
				Uplink:   traffic.DirectionReset{Effect: traffic.ResetAcknowledged, PreviousBytes: math.MaxInt64},
				Downlink: traffic.DirectionReset{Effect: traffic.ResetUnknown},
			},
			err: traffic.ErrResetUnknown, calls: 2,
		},
		{
			name: "uplink missing", first: codes.NotFound,
			want: traffic.PairReset{
				Uplink:   traffic.DirectionReset{Effect: traffic.ResetMissing},
				Downlink: traffic.DirectionReset{Effect: traffic.ResetAcknowledged, PreviousBytes: math.MinInt64},
			},
			calls: 2,
		},
		{
			name: "execution replaced", replaced: true,
			want: traffic.PairReset{
				Uplink:   traffic.DirectionReset{Effect: traffic.ResetAcknowledged, PreviousBytes: math.MaxInt64},
				Downlink: traffic.DirectionReset{Effect: traffic.ResetNotAttempted},
			},
			err: traffic.ErrUnavailable, calls: 1,
		},
		{
			name: "first reply lost", first: codes.Unavailable,
			want: traffic.PairReset{
				Uplink:   traffic.DirectionReset{Effect: traffic.ResetUnknown},
				Downlink: traffic.DirectionReset{Effect: traffic.ResetNotAttempted},
			},
			err: traffic.ErrResetUnknown, calls: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			var replaced atomic.Bool
			conn := startResetTestServer(t, func(_ any, stream grpc.ServerStream) error {
				var request ResetCounterRequest
				if err := stream.RecvMsg(&request); err != nil {
					return err
				}
				const prefix = "inbound>>>team>>>edge>>>traffic>>>"
				var direction string
				var bytes int64
				var code codes.Code
				switch calls.Add(1) {
				case 1:
					direction, bytes, code = "uplink", math.MaxInt64, test.first
					replaced.Store(test.replaced)
				case 2:
					direction, bytes, code = "downlink", math.MinInt64, test.second
				default:
					return status.Error(codes.Internal, "unexpected extra native call")
				}
				if request.GetName() != prefix+direction || !request.GetReset_() {
					return status.Error(codes.InvalidArgument, "wrong selected counter")
				}
				if code != codes.OK {
					return status.Error(code, "fixture native failure")
				}
				return stream.SendMsg(&NamedCounterResponse{Stat: &TrafficCounter{Name: request.Name, Value: bytes}})
			})
			client := NewTraffic(replacingExecution{conn, &replaced}, false)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			got, err := client.ResetPair(ctx, traffic.CounterPair{ID: "primary", Scope: traffic.Inbound, Name: "team>>>edge"})
			if got != test.want || !errors.Is(err, test.err) || calls.Load() != test.calls {
				t.Fatalf("pair reset = (%+v, %v), calls %d, want (%+v, %v), calls %d",
					got, err, calls.Load(), test.want, test.err, test.calls)
			}
		})
	}
}
