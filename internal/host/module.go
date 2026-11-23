// Package host exposes observations from the local operating system.
package host

import (
	hostgrpc "github.com/PapilioVPN/node/internal/host/bridge/grpc"
	hosthttp "github.com/PapilioVPN/node/internal/host/bridge/http"
	"github.com/PapilioVPN/node/internal/host/services"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

var Module = fx.Module("host",
	fx.Provide(services.NewCPUSampler),
	fx.Invoke(registerCPUSampler, register),
)

func register(httpRouter *chi.Mux, grpcServer *grpc.Server, sampler *services.CPUSampler, config settings.Host) error {
	path := config.StoragePath
	if path == "" {
		path = "/"
	}
	if err := hostgrpc.Register(grpcServer, sampler, path); err != nil {
		return err
	}
	hosthttp.Register(httpRouter, sampler, path)
	return nil
}

func registerCPUSampler(lifecycle fx.Lifecycle, sampler *services.CPUSampler) {
	lifecycle.Append(fx.Hook{OnStart: sampler.Start, OnStop: sampler.Stop})
}
