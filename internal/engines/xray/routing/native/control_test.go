package native

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray/routing/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestUnacknowledgedMutationFencesExecution(t *testing.T) {
	var applied atomic.Int32
	first := mutationFixture(t, func(_ context.Context, req *OverrideBalancerTargetRequest) error {
		if req.GetBalancerTag() == "missing" {
			return status.Error(codes.Unknown, "app/router: cannot find tag")
		}
		applied.Add(1)
		return status.Error(codes.Unavailable, "lost acknowledgement")
	})
	second := mutationFixture(t, func(_ context.Context, _ *OverrideBalancerTargetRequest) error {
		applied.Add(1)
		return nil
	})
	instance := &activeFixture{conn: first}
	client := New(instance)

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := client.OverrideBalancerTarget(canceled, "primary", "pool", "out"); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-dispatch cancellation = %v", err)
	}
	if err := client.OverrideBalancerTarget(t.Context(), "primary", "missing", "out"); !errors.Is(err, state.ErrBalancerNotFound) {
		t.Fatalf("known rejection = %v", err)
	}
	if err := client.OverrideBalancerTarget(t.Context(), "primary", "pool", "out"); !errors.Is(err, state.ErrMutationUnknown) {
		t.Fatalf("lost acknowledgement = %v", err)
	}
	if err := client.OverrideBalancerTarget(t.Context(), "primary", "pool", ""); !errors.Is(err, state.ErrMutationFenced) {
		t.Fatalf("mutation after uncertain effect = %v", err)
	}
	if err := client.RemoveRoutingRule(t.Context(), "primary", "rule"); !errors.Is(err, state.ErrMutationFenced) {
		t.Fatalf("rule removal after uncertain override = %v", err)
	}
	if got := applied.Load(); got != 1 {
		t.Fatalf("first execution received %d effects, want one", got)
	}

	instance.conn = second
	if err := client.OverrideBalancerTarget(t.Context(), "primary", "pool", ""); err != nil {
		t.Fatal(err)
	}
	if got := applied.Load(); got != 2 {
		t.Fatalf("replacement execution did not accept the next request, got %d effects", got)
	}
}

func TestRuleMutationCannotOverlapOverride(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	conn := mutationFixture(t, func(ctx context.Context, _ *OverrideBalancerTargetRequest) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	client := New(&activeFixture{conn: conn})
	result := make(chan error, 1)
	go func() { result <- client.OverrideBalancerTarget(ctx, "primary", "pool", "out") }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("override did not enter native server")
	}
	err := client.RemoveRoutingRule(ctx, "primary", "rule")
	close(release)
	if !errors.Is(err, state.ErrMutationBusy) {
		t.Fatalf("overlapping rule removal = %v", err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

type activeFixture struct{ conn *grpc.ClientConn }

func (f *activeFixture) NativeConnection(string) (*grpc.ClientConn, error) { return f.conn, nil }

func mutationFixture(t *testing.T, invoke func(context.Context, *OverrideBalancerTargetRequest) error) *grpc.ClientConn {
	t.Helper()
	listener := bufconn.Listen(64 << 10)
	server := grpc.NewServer(grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
		method, ok := grpc.MethodFromServerStream(stream)
		if !ok || method != overrideMethod {
			return status.Error(codes.Unimplemented, "unexpected fixture method")
		}
		var request OverrideBalancerTargetRequest
		if err := stream.RecvMsg(&request); err != nil {
			return err
		}
		if err := invoke(stream.Context(), &request); err != nil {
			return err
		}
		return stream.SendMsg(&emptypb.Empty{})
	}))
	done := make(chan struct{})
	go func() {
		_ = server.Serve(listener)
		close(done)
	}()
	t.Cleanup(func() {
		server.Stop()
		<-done
	})
	conn, err := grpc.NewClient("passthrough:///fixture", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
