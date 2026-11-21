//go:build integration

package rpc

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	health "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
)

type countingListener struct {
	net.Listener
	connections atomic.Int32
}

func (l *countingListener) Accept() (net.Conn, error) {
	connection, err := l.Listener.Accept()
	if err == nil {
		l.connections.Add(1)
	}
	return connection, err
}

type healthFixture struct {
	health.UnimplementedHealthServer
	entered  chan struct{}
	canceled chan struct{}
}

func (h *healthFixture) Check(ctx context.Context, r *health.HealthCheckRequest) (*health.HealthCheckResponse, error) {
	if r.Service == "blocked" {
		close(h.entered)
		<-ctx.Done()
		close(h.canceled)
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	result := &health.HealthCheckResponse{Status: health.HealthCheckResponse_SERVING}
	if r.Service == "large" {
		unknown := protowire.AppendTag(nil, 100, protowire.BytesType)
		result.ProtoReflect().SetUnknown(protowire.AppendBytes(unknown, make([]byte, 70<<10)))
	}
	return result, nil
}
func TestOneNativeChannelReusedAndCloseCancelsInFlightWork(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	counted := &countingListener{Listener: listener}
	server := grpc.NewServer()
	fixture := &healthFixture{entered: make(chan struct{}), canceled: make(chan struct{})}
	health.RegisterHealthServer(server, fixture)
	stopped := make(chan struct{})
	go func() { defer close(stopped); server.Serve(counted) }()
	t.Cleanup(func() { server.Stop(); <-stopped })
	connection, err := New(Config{Network: "tcp", Address: listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	client := health.NewHealthClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for range 20 {
		if _, err := client.Check(ctx, &health.HealthCheckRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.Check(ctx, &health.HealthCheckRequest{Service: "large"}); status.Code(err) != codes.ResourceExhausted {
		t.Fatal("native response byte ceiling not enforced", err)
	}
	if counted.connections.Load() != 1 {
		t.Fatal("native calls created additional channels")
	}
	done := make(chan error, 1)
	go func() { _, err := client.Check(ctx, &health.HealthCheckRequest{Service: "blocked"}); done <- err }()
	select {
	case <-fixture.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if status.Code(err) != codes.Canceled {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("close did not interrupt native call")
	}
	select {
	case <-fixture.canceled:
	case <-ctx.Done():
		t.Fatal("native server did not observe cancellation")
	}
	if _, err := client.Check(ctx, &health.HealthCheckRequest{}); !errors.Is(err, ErrClosed) {
		t.Fatal("closed owner admitted another call", err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
}
