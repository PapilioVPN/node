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
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	health "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

func TestReadinessCanReachReplacementDuringNativeBackoff(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	health.RegisterHealthServer(server, &healthFixture{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		<-stopped
	})
	var available atomic.Bool
	// Model a local endpoint that disappears between owned executions. Give
	// gRPC a known backoff so scheduling cannot hide a missed readiness budget.
	channel, err := grpc.NewClient("passthrough:///"+listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff:           backoff.Config{BaseDelay: 10 * time.Second, Multiplier: 1, MaxDelay: 10 * time.Second},
			MinConnectTimeout: time.Second,
		}),
		grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
			if !available.Load() {
				return nil, errors.New("fixture endpoint unavailable")
			}
			return (&net.Dialer{}).DialContext(ctx, "tcp", address)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{connection: channel}
	t.Cleanup(func() { _ = client.Close() })
	native := health.NewHealthClient(client)
	initial, cancelInitial := context.WithTimeout(t.Context(), time.Second)
	defer cancelInitial()
	if _, err := native.Check(initial, &health.HealthCheckRequest{}); status.Code(err) != codes.Unavailable {
		t.Fatalf("outage did not enter native backoff: %v", err)
	}
	if state := channel.GetState(); state != connectivity.TransientFailure {
		t.Fatalf("outage channel state = %v", state)
	}

	available.Store(true)
	client.ResetConnectBackoff()
	probe, cancelProbe := context.WithTimeout(t.Context(), time.Second)
	defer cancelProbe()
	if _, err := native.Check(probe, &health.HealthCheckRequest{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("replacement missed the readiness deadline: %v", err)
	}
}
