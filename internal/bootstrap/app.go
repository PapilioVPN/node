package bootstrap

import (
	"context"
	"errors"
	"log/slog"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"

	"github.com/PapilioVPN/node/internal/apps/system"
	rpc "github.com/PapilioVPN/node/internal/entrypoints/grpc"
	"github.com/PapilioVPN/node/internal/settings"
)

// New composes validated settings and the implemented capability modules.
// Options use native Fx extension points, including composition test consumers.
func New(cfg settings.Config, version string, options ...fx.Option) *fx.App {
	modules := []fx.Option{
		fx.Supply(cfg), system.Module(version),
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
	application := New(cfg, version, append(options, fx.Populate(&server))...)
	defer func() {
		if server != nil {
			result = errors.Join(result, server.Close())
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
