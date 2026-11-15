package bootstrap

import (
	"context"
	"github.com/gofiber/fiber/v3"
	"io"

	"go.uber.org/fx"
	"google.golang.org/grpc"

	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/platform/httpserver"
	"github.com/PapilioVPN/node/internal/settings"
)

// A transport owns admission and joins its native serving work before modules close.
// Capability modules still own registration and application operations.
type transport interface {
	Start(context.Context) error
	Stop(context.Context) error
	io.Closer
}

func registerTransports(lifecycle fx.Lifecycle, resources moduleResources) {
	for _, server := range resources.Transports {
		lifecycle.Append(fx.Hook{OnStart: server.Start, OnStop: server.Stop})
	}
}

func communications(cfg settings.Communications) fx.Option {
	var options []fx.Option
	if cfg.GRPC.Enabled {
		options = append(options, fx.Supply(cfg.GRPC, cfg.GRPC.Permissions), fx.Provide(newGRPCServer))
	}
	if cfg.HTTP.Enabled {
		options = append(options, fx.Supply(cfg.HTTP), fx.Provide(newHTTPServer))
	}
	return fx.Options(fx.Options(options...), fx.Invoke(registerTransports))
}

type grpcResult struct {
	fx.Out
	Server    *rpc.Server
	Registrar grpc.ServiceRegistrar
	Transport transport `group:"transports"`
}

func newGRPCServer(cfg settings.GRPC, shutdown fx.Shutdowner) (grpcResult, error) {
	server, err := rpc.NewServer(cfg, func() {
		// A second shutdown request needs no further action; Stop reports the cause.
		_ = shutdown.Shutdown(fx.ExitCode(1))
	})
	if err != nil {
		return grpcResult{}, err
	}
	return grpcResult{Server: server, Registrar: server.Registrar(), Transport: server}, nil
}

type httpResult struct {
	fx.Out
	Server    *httpserver.Server
	Router    fiber.Router
	Transport transport `group:"transports"`
}

func newHTTPServer(cfg settings.HTTP, shutdown fx.Shutdowner) (httpResult, error) {
	server, err := httpserver.NewServer(cfg, func() { _ = shutdown.Shutdown(fx.ExitCode(1)) })
	if err != nil {
		return httpResult{}, err
	}
	return httpResult{Server: server, Router: server.Router(), Transport: server}, nil
}
