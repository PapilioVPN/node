// Package host exposes observations from the local operating system.
package host

import (
	hostgrpc "github.com/PapilioVPN/node/internal/host/bridge/grpc"
	hosthttp "github.com/PapilioVPN/node/internal/host/bridge/http"
	"github.com/PapilioVPN/node/internal/host/cpu"
	"github.com/PapilioVPN/node/internal/host/network"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

var Module = fx.Module("host",
	fx.Provide(cpu.NewSampler, network.NewSampler),
	fx.Invoke(registerSamplers, register),
)

func register(httpRouter *chi.Mux, grpcServer *grpc.Server, sampler *cpu.Sampler, netSampler *network.Sampler, config settings.Host) error {
	path := config.StoragePath
	if path == "" {
		path = "/"
	}
	if err := hostgrpc.Register(grpcServer, sampler, netSampler, path); err != nil {
		return err
	}
	hosthttp.Register(httpRouter, sampler, netSampler, path)
	return nil
}

func registerSamplers(lifecycle fx.Lifecycle, sampler *cpu.Sampler, netSampler *network.Sampler) {
	lifecycle.Append(fx.Hook{OnStart: sampler.Start, OnStop: sampler.Stop})
	lifecycle.Append(fx.Hook{OnStart: netSampler.Start, OnStop: netSampler.Stop})
}
