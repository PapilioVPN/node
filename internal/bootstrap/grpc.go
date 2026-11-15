package bootstrap

import (
	"go.uber.org/fx"
	"google.golang.org/grpc"

	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
)

// The process-wide transport is registered here; its adapter has no Fx imports.
var grpcModule = fx.Module("grpc", fx.Provide(newGRPCServer, serviceRegistrar), fx.Invoke(registerGRPCLifecycle))

func newGRPCServer(cfg settings.GRPC, shutdown fx.Shutdowner) (*rpc.Server, error) {
	return rpc.NewServer(cfg, func() {
		// A second shutdown request needs no further action; Stop reports the cause.
		_ = shutdown.Shutdown(fx.ExitCode(1))
	})
}

func serviceRegistrar(server *rpc.Server) grpc.ServiceRegistrar { return server.Registrar() }

func registerGRPCLifecycle(lifecycle fx.Lifecycle, server *rpc.Server) {
	lifecycle.Append(fx.Hook{OnStart: server.Start, OnStop: server.Stop})
}
