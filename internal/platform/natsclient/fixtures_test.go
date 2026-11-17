//go:build integration

package natsclient_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/nats-io/nats.go"
	"go.uber.org/fx"
)

func clientConfig(address string, files mtls.Files) settings.NATS {
	return settings.NATS{Servers: []string{address}, TLS: files, TLSHandshakeFirst: true,
		ConnectTimeout: time.Second, ShutdownTimeout: time.Second, WriteTimeout: time.Second,
		ReconnectWait: 20 * time.Millisecond, ReconnectJitter: 0, MaxReconnects: 2, ReconnectBufferSize: 1024,
		PingInterval: time.Second, MaxPingsOutstanding: 2}
}

func composeClient(t *testing.T, config settings.NATS, options ...fx.Option) (*fx.App, *natsclient.Client) {
	t.Helper()
	var client *natsclient.Client
	base := []fx.Option{fx.NopLogger, fx.Supply(config, slog.New(slog.NewTextHandler(io.Discard, nil))), natsclient.Module, fx.Populate(&client)}
	app := fx.New(append(base, options...)...)
	if err := app.Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = app.Stop(ctx)
	})
	return app, client
}

func startClient(t *testing.T, app *fx.App) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
}

func stopClient(t *testing.T, app *fx.App) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := app.Stop(ctx); err != nil {
		t.Errorf("stop NATS: %v", err)
	}
}

func connection(t *testing.T, client *natsclient.Client) *nats.Conn {
	t.Helper()
	conn, err := client.Conn()
	if err != nil {
		t.Fatal(err)
	}
	return conn
}
