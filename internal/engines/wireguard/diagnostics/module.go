// Package diagnostics exposes WireGuard logs and runtime observations.
package diagnostics

import (
	bridgegrpc "github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics/bridge/grpc"
	bridgehttp "github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics/bridge/http"
	"github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics/events"
	"github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics/process"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

var Module = fx.Module("wireguard-diagnostics", fx.Provide(newLogs, func() *process.Runtime { return new(process.Runtime) }), fx.Invoke(register))

func newLogs(lifecycle fx.Lifecycle, config settings.WireGuard) *events.Service {
	service := events.New(config.Interface)
	lifecycle.Append(fx.Hook{OnStop: service.Close})
	return service
}

func register(config settings.WireGuard, router *chi.Mux, server *grpc.Server, logs *events.Service, runtime *process.Runtime) {
	if config.Enabled() {
		bridgehttp.Register(router, logs, runtime)
		bridgegrpc.Register(server, logs, runtime)
	}
}
