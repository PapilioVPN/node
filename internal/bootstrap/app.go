package bootstrap

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"

	"github.com/PapilioVPN/node/internal/modules/core"
	"github.com/PapilioVPN/node/internal/modules/engines"
	"github.com/PapilioVPN/node/internal/modules/system"
	"github.com/PapilioVPN/node/internal/settings"
)

// New composes validated settings and the implemented capability modules.
// Options use native Fx extension points, including composition test consumers.
// Run owns final cleanup when an expired Fx context prevents lifecycle hooks.
func New(cfg settings.Config, version string, options ...fx.Option) *fx.App {
	return fx.New(
		fx.Supply(cfg.Node, cfg.Instances),
		system.Module(version, cfg.Communications), core.Module(cfg.Communications.GRPC.Enabled),
		engines.Module(cfg.Communications.GRPC.Enabled),
		communications(cfg.Communications),
		fx.StartTimeout(cfg.Runtime.StartupTimeout), fx.StopTimeout(cfg.Runtime.ShutdownTimeout),
		fx.WithLogger(func() fxevent.Logger { return &fxevent.SlogLogger{Logger: slog.Default()} }),
		fx.Options(options...),
	)
}

// Run starts the process and stops it on caller cancellation or native Fx shutdown.
func Run(ctx context.Context, cfg settings.Config, version string, options ...fx.Option) (result error) {
	if !cfg.Communications.GRPC.Enabled && !cfg.Communications.HTTP.Enabled && !cfg.Communications.NATS.Enabled {
		return errors.New("no implemented communication transport is enabled")
	}
	var resources moduleResources
	application := New(cfg, version, append(options, fx.Populate(&resources))...)
	defer func() {
		for _, server := range resources.Transports {
			result = errors.Join(result, server.Close())
		}
		for _, resource := range resources.Closers {
			result = errors.Join(result, resource.Close())
		}
	}()
	startup, cancel := context.WithTimeout(ctx, cfg.Runtime.StartupTimeout)
	err := application.Start(startup)
	cancel()
	if err != nil {
		return err
	}
	var exitCode int
	select {
	case <-ctx.Done():
	case signal := <-application.Wait():
		exitCode = signal.ExitCode
	}
	shutdown, cancel := context.WithTimeout(context.Background(), cfg.Runtime.ShutdownTimeout)
	defer cancel()
	err = application.Stop(shutdown)
	if exitCode != 0 {
		return errors.Join(errors.New("runtime stopped unexpectedly"), err)
	}
	return err
}

// Module resources are independent owners; each preserves its internal close order.
type moduleResources struct {
	fx.In
	Transports []transport `group:"transports"`
	Closers    []io.Closer `group:"module_resources"`
}
