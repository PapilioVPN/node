// Package bootstrap connects the Node process to its shared infrastructure.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/PapilioVPN/node/internal/platform/management"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
)

const lifecycleTimeout = 5 * time.Second

func Run(ctx context.Context, config settings.Config, logger *slog.Logger) error {
	app := fx.New(
		fx.Supply(config.Management, logger),
		fx.Provide(management.New),
		fx.Invoke(registerManagement),
		fx.WithLogger(func() fxevent.Logger { return &fxevent.SlogLogger{Logger: logger} }),
	)
	startCtx, cancelStart := context.WithTimeout(ctx, lifecycleTimeout)
	err := app.Start(startCtx)
	cancelStart()
	if err != nil {
		return err
	}

	var runErr error
	select {
	case <-ctx.Done():
	case signal := <-app.Wait():
		if signal.ExitCode != 0 {
			runErr = fmt.Errorf("node requested shutdown with exit code %d", signal.ExitCode)
		}
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), lifecycleTimeout)
	defer cancel()
	return errors.Join(runErr, app.Stop(stopCtx))
}

func registerManagement(lifecycle fx.Lifecycle, servers *management.Servers) {
	lifecycle.Append(fx.Hook{OnStart: servers.Start, OnStop: servers.Stop})
}
