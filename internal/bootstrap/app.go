// Package bootstrap composes Node's installed infrastructure and capabilities.
package bootstrap

import (
	"log/slog"

	"github.com/PapilioVPN/node/internal/apps/system"
	"github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/platform/httpserver"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/shutdown"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
)

func New(config settings.Config, logger *slog.Logger) *fx.App {
	options := []fx.Option{
		fx.Supply(logger),
		fx.Decorate(shutdown.Synchronize),
		fx.StartTimeout(config.StartupTimeout),
		fx.StopTimeout(config.ShutdownTimeout),
		fx.WithLogger(func() fxevent.Logger { return &fxevent.SlogLogger{Logger: logger} }),
	}
	if http := config.Communications.HTTP; http != nil {
		options = append(options, fx.Supply(*http), httpserver.Module)
	}
	if grpc := config.Communications.GRPC; grpc != nil {
		options = append(options, fx.Supply(*grpc), grpcserver.Module)
	}
	if nats := config.Communications.NATS; nats != nil {
		options = append(options, fx.Supply(*nats), natsclient.Module)
		if config.Communications.Requests.NATS != nil {
			options = append(options, natsclient.RequestsModule(*config.Communications.Requests.NATS, config.RequestDrainBudget()))
		}
	}
	return fx.New(append(options, system.Module(config.Communications.Requests, config.Sessions))...)
}
