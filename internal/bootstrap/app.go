package bootstrap

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"

	"github.com/PapilioVPN/node/internal/modules/engines/xray"
	"github.com/PapilioVPN/node/internal/modules/instances"
	"github.com/PapilioVPN/node/internal/modules/system"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
)

// New composes validated settings and the implemented capability modules.
// Options use native Fx extension points, including composition test consumers.
// Run owns final cleanup when an expired Fx context prevents lifecycle hooks.
func New(cfg settings.Config, version string, options ...fx.Option) *fx.App {
	modules := []fx.Option{
		fx.Supply(cfg.Node, cfg.Communications.GRPC, cfg.Communications.GRPC.Permissions, cfg.Instances),
		system.Module(version, cfg.Communications.GRPC.Enabled), instances.Module(cfg.Communications.GRPC.Enabled),
		xray.Module(cfg.Communications.GRPC.Enabled),
		fx.StartTimeout(cfg.Runtime.StartupTimeout), fx.StopTimeout(cfg.Runtime.ShutdownTimeout),
		fx.WithLogger(func() fxevent.Logger { return &fxevent.SlogLogger{Logger: slog.Default()} }),
	}
	if cfg.Communications.GRPC.Enabled {
		modules = append(modules, grpcModule)
	}
	return fx.New(append(modules, options...)...)
}

// Run starts the process and stops it on caller cancellation or native Fx shutdown.
func Run(ctx context.Context, cfg settings.Config, version string, options ...fx.Option) (result error) {
	if !cfg.Communications.GRPC.Enabled {
		return errors.New("no implemented communication transport is enabled")
	}
	var server *rpc.Server
	var resources moduleResources
	application := New(cfg, version, append(options, fx.Populate(&server, &resources))...)
	defer func() {
		if server != nil {
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
	Closers []io.Closer `group:"module_resources"`
}
