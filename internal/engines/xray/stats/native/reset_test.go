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
	listener := bufconn.Listen(64 << 10)
	var calls atomic.Int32
	var bytes atomic.Int64
	bytes.Store(math.MaxInt64)
	server := grpc.NewServer(grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
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
	}))
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
	client := NewTraffic(resetConnection{conn})
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
