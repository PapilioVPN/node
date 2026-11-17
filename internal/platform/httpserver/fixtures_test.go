//go:build integration

package httpserver_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/platform/httpserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
)

func serverConfig(files mtls.Files) settings.HTTP {
	return settings.HTTP{
		Address: "127.0.0.1:0", TLS: files,
		ReadTimeout: time.Second, WriteTimeout: time.Second,
		IdleTimeout: time.Second, ShutdownTimeout: time.Second,
		BodyLimit: 64, Concurrency: 16,
	}
}

func composeServer(t *testing.T, config settings.HTTP, options ...fx.Option) (*fx.App, *fiber.App, <-chan string) {
	t.Helper()
	var server *fiber.App
	address := make(chan string, 1)
	base := []fx.Option{
		fx.NopLogger,
		fx.Supply(config, slog.New(slog.NewTextHandler(io.Discard, nil))),
		httpserver.Module,
		fx.Populate(&server),
		fx.Invoke(func(app *fiber.App) {
			app.Hooks().OnListen(func(data fiber.ListenData) error {
				address <- net.JoinHostPort(data.Host, data.Port)
				return nil
			})
		}),
	}
	app := fx.New(append(base, options...)...)
	if err := app.Err(); err != nil {
		t.Fatal(err)
	}
	return app, server, address
}

func stopServer(t *testing.T, app *fx.App) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := app.Stop(ctx); err != nil {
		t.Errorf("stop HTTP resource: %v", err)
	}
}
