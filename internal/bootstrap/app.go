// Package bootstrap composes Node's installed infrastructure and capabilities.
package bootstrap

import (
	"log/slog"

	"github.com/PapilioVPN/node/internal/platform/httpserver"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
)

func New(config settings.Config, logger *slog.Logger) *fx.App {
	options := []fx.Option{
		fx.Supply(logger),
		fx.StartTimeout(config.StartupTimeout),
		fx.StopTimeout(config.ShutdownTimeout),
		fx.WithLogger(func() fxevent.Logger { return &fxevent.SlogLogger{Logger: logger} }),
	}
	if http := config.Communications.HTTP; http != nil {
		options = append(options, fx.Supply(*http), httpserver.Module)
	}
	return fx.New(options...)
}
