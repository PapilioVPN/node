//go:build integration

package grpcserver_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
)

func serverConfig(files mtls.Files) settings.GRPC {
	return settings.GRPC{Address: "127.0.0.1:0", TLS: files, ConnectionTimeout: time.Second, ShutdownTimeout: time.Second,
		MaxRecvMessageSize: 64, MaxSendMessageSize: 64, MaxConnections: 16, MaxConcurrentStreams: 8, MaxHeaderListSize: 8192}
}

type addressLog struct {
	slog.Handler
	address chan string
}

func (log addressLog) Handle(_ context.Context, record slog.Record) error {
	if record.Message == "gRPC listening" {
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Key == "address" {
				log.address <- attr.Value.String()
			}
			return true
		})
	}
	return nil
}

func composeServer(t *testing.T, config settings.GRPC, options ...fx.Option) (*fx.App, *grpc.Server, <-chan string) {
	t.Helper()
	address := make(chan string, 1)
	logger := slog.New(addressLog{slog.NewTextHandler(io.Discard, nil), address})
	var server *grpc.Server
	base := []fx.Option{fx.NopLogger, fx.Supply(config, logger), grpcserver.Module, fx.Populate(&server),
		fx.Invoke(func(server *grpc.Server) { healthv1.RegisterHealthServer(server, health.NewServer()) })}
	app := fx.New(append(base, options...)...)
	if err := app.Err(); err != nil {
		t.Fatal(err)
	}
	return app, server, address
}

func startServer(t *testing.T, app *fx.App, address <-chan string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return <-address
}

func stopServer(t *testing.T, app *fx.App) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := app.Stop(ctx); err != nil {
		t.Errorf("stop gRPC: %v", err)
	}
}

func connect(t *testing.T, address string, files mtls.Files) *grpc.ClientConn {
	t.Helper()
	identity, err := mtls.Client(files)
	if err != nil {
		t.Fatal(err)
	}
	client, err := grpc.NewClient(address, grpc.WithTransportCredentials(credentials.NewTLS(identity)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}
