//go:build integration

package grpcserver_test

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

func TestMutualTLSAndMessageLimits(t *testing.T) {
	pki := testutil.NewPKI(t)
	app, _, address := composeServer(t, serverConfig(pki.Server))
	bound := startServer(t, app, address)
	defer stopServer(t, app)
	client := healthv1.NewHealthClient(connect(t, bound, pki.Client))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := client.Check(ctx, &healthv1.HealthCheckRequest{})
	if err != nil || result.GetStatus() != healthv1.HealthCheckResponse_SERVING {
		t.Fatalf("trusted RPC: %v, %v", result, err)
	}
	_, err = client.Check(ctx, &healthv1.HealthCheckRequest{Service: strings.Repeat("x", 65)})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("receive limit: %v", err)
	}

	foreign := testutil.NewPKI(t)
	for _, test := range []struct {
		name   string
		files  mtls.Files
		change func(*tls.Config)
	}{
		{"missing client certificate", pki.Client, func(config *tls.Config) { config.Certificates = nil }},
		{"foreign client certificate", pki.Client, func(config *tls.Config) {
			key, err := tls.LoadX509KeyPair(foreign.Client.Certificate, foreign.Client.PrivateKey)
			if err != nil {
				t.Fatal(err)
			}
			config.Certificates = []tls.Certificate{key}
		}},
		{"untrusted server", foreign.Client, nil},
		{"wrong server name", pki.Client, func(config *tls.Config) { config.ServerName = "wrong.invalid" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			identity, err := mtls.Client(test.files)
			if err != nil {
				t.Fatal(err)
			}
			if test.change != nil {
				test.change(identity)
			}
			conn, err := grpc.NewClient(bound, grpc.WithTransportCredentials(credentials.NewTLS(identity)))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			if _, err := healthv1.NewHealthClient(conn).Check(ctx, &healthv1.HealthCheckRequest{}); err == nil {
				t.Fatal("invalid peer accepted")
			}
		})
	}
}

func TestSendLimit(t *testing.T) {
	pki := testutil.NewPKI(t)
	config := serverConfig(pki.Server)
	config.MaxSendMessageSize = 1
	app, _, address := composeServer(t, config)
	bound := startServer(t, app, address)
	defer stopServer(t, app)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := healthv1.NewHealthClient(connect(t, bound, pki.Client)).Check(ctx, &healthv1.HealthCheckRequest{})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("send limit: %v", err)
	}
}

func TestShutdownCancelsLongLivedStreamAfterDrainBudget(t *testing.T) {
	pki := testutil.NewPKI(t)
	config := serverConfig(pki.Server)
	config.ShutdownTimeout = 750 * time.Millisecond
	app, _, address := composeServer(t, config)
	bound := startServer(t, app, address)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := healthv1.NewHealthClient(connect(t, bound, pki.Client)).Watch(ctx, &healthv1.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	if err := app.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("forced stop must report drain deadline: %v", err)
	}
	if _, err := stream.Recv(); err == nil {
		t.Fatal("stream remains open after forced shutdown")
	}
}

func TestBindFailureAndStartupRollback(t *testing.T) {
	pki := testutil.NewPKI(t)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	config := serverConfig(pki.Server)
	config.Address = occupied.Addr().String()
	app, _, _ := composeServer(t, config)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := app.Start(ctx); err == nil {
		t.Fatal("occupied port accepted")
	}

	failure := errors.New("later startup failed")
	app, _, address := composeServer(t, serverConfig(pki.Server), fx.Invoke(func(lifecycle fx.Lifecycle, _ *grpc.Server) {
		lifecycle.Append(fx.Hook{OnStart: func(context.Context) error { return failure }})
	}))
	if err := app.Start(ctx); !errors.Is(err, failure) {
		t.Fatalf("startup failure lost: %v", err)
	}
	listener, err := net.Listen("tcp", <-address)
	if err != nil {
		t.Fatalf("rolled-back listener remains open: %v", err)
	}
	listener.Close()
}

func TestUnexpectedStopRequestsFatalExit(t *testing.T) {
	pki := testutil.NewPKI(t)
	app, server, address := composeServer(t, serverConfig(pki.Server))
	startServer(t, app, address)
	server.Stop()
	select {
	case signal := <-app.Wait():
		if signal.ExitCode != 1 {
			t.Fatalf("unexpected exit code: %d", signal.ExitCode)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("unexpected server exit was not propagated")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := app.Stop(ctx); err == nil {
		t.Fatal("unexpected exit cause was lost")
	}
}

func TestStreamLimitQueuesWorkUntilCapacityIsAvailable(t *testing.T) {
	pki := testutil.NewPKI(t)
	config := serverConfig(pki.Server)
	config.MaxConcurrentStreams = 1
	app, _, address := composeServer(t, config)
	bound := startServer(t, app, address)
	defer stopServer(t, app)
	client := healthv1.NewHealthClient(connect(t, bound, pki.Client))
	streamContext, cancelStream := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStream()
	stream, err := client.Watch(streamContext, &healthv1.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	limitedContext, cancelLimited := context.WithTimeout(context.Background(), 100*time.Millisecond)
	_, err = client.Check(limitedContext, &healthv1.HealthCheckRequest{})
	cancelLimited()
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("stream capacity was exceeded: %v", err)
	}
	cancelStream()
	if _, err := stream.Recv(); err == nil {
		t.Fatal("stream did not cancel")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := client.Check(ctx, &healthv1.HealthCheckRequest{}); err != nil {
		t.Fatalf("stream capacity did not recover: %v", err)
	}
}

func TestConnectionLimitReleasesCapacityOnClose(t *testing.T) {
	pki := testutil.NewPKI(t)
	config := serverConfig(pki.Server)
	config.MaxConnections = 1
	app, _, address := composeServer(t, config)
	bound := startServer(t, app, address)
	defer stopServer(t, app)
	first := connect(t, bound, pki.Client)
	second := connect(t, bound, pki.Client)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := healthv1.NewHealthClient(first).Check(ctx, &healthv1.HealthCheckRequest{}); err != nil {
		t.Fatal(err)
	}
	limitedContext, cancelLimited := context.WithTimeout(ctx, 100*time.Millisecond)
	_, err := healthv1.NewHealthClient(second).Check(limitedContext, &healthv1.HealthCheckRequest{})
	cancelLimited()
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("connection capacity was exceeded: %v", err)
	}
	first.Close()
	if _, err := healthv1.NewHealthClient(second).Check(ctx, &healthv1.HealthCheckRequest{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("connection capacity did not recover: %v", err)
	}
	second.Close()
}
