// Package host exposes observations from the local operating system.
package host

import (
	"github.com/PapilioVPN/node/internal/host/bridge"
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
	return bridge.Register(httpRouter, grpcServer, sampler, path)
}

func registerCPUSampler(lifecycle fx.Lifecycle, sampler *services.CPUSampler) {
	lifecycle.Append(fx.Hook{OnStart: sampler.Start, OnStop: sampler.Stop})
}
