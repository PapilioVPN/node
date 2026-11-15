package bootstrap

import (
	"go.uber.org/fx"

	rpc "github.com/PapilioVPN/node/internal/entrypoints/grpc"
	"github.com/PapilioVPN/node/internal/modules/system/app"
	"github.com/PapilioVPN/node/internal/settings"
)

// The process-wide transport is registered here; its adapter has no Fx imports.
var grpcModule = fx.Module("grpc", fx.Provide(newGRPCServer), fx.Invoke(registerGRPCLifecycle))

func newGRPCServer(cfg settings.Config, info *app.InfoService, shutdown fx.Shutdowner) (*rpc.Server, error) {
	return rpc.NewServer(cfg.Communications.GRPC, info, func() {
		// A second shutdown request needs no further action; Stop reports the cause.
		_ = shutdown.Shutdown(fx.ExitCode(1))
	})
}

func registerGRPCLifecycle(lifecycle fx.Lifecycle, server *rpc.Server) {
	lifecycle.Append(fx.Hook{OnStart: server.Start, OnStop: server.Stop})
}
