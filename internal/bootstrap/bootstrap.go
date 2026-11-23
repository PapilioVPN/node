// Package bootstrap connects the Node process to its shared infrastructure.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray"
	"github.com/PapilioVPN/node/internal/host"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
)

const lifecycleTimeout = 5 * time.Second

func Run(ctx context.Context, config settings.Config, logger *slog.Logger) error {
	app := fx.New(
		fx.Supply(config.Management, config.Host, config.Xray, logger),
		fx.Provide(loadTLS, newReadiness, newHTTPRouter, newHTTPServer, newGRPCServer),
		host.Module,
		xray.Module,
		fx.Invoke(registerListeners),
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
