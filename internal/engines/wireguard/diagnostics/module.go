// Package diagnostics exposes WireGuard logs and runtime observations.
package diagnostics

import (
	bridgegrpc "github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics/bridge/grpc"
	bridgehttp "github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics/bridge/http"
	"github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics/events"
	"github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics/probe"
	"github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics/process"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"time"
)

var Module = fx.Module("wireguard-diagnostics", fx.Provide(newLogs, newProbe, func() *process.Runtime { return new(process.Runtime) }), fx.Invoke(register))

func newProbe(config settings.WireGuard) *probe.Service {
	if config.Probe == nil {
		return probe.New("", "", 0)
	}
	options := config.Probe
	return probe.New(options.URL, options.Interface, time.Duration(options.TimeoutMilliseconds)*time.Millisecond)
}

func newLogs(lifecycle fx.Lifecycle, config settings.WireGuard) *events.Service {
	service := events.New(config.Interface)
	lifecycle.Append(fx.Hook{OnStop: service.Close})
	return service
}

func register(config settings.WireGuard, router *chi.Mux, server *grpc.Server, logs *events.Service, runtime *process.Runtime, check *probe.Service) {
	if config.Enabled() {
		bridgehttp.Register(router, logs, runtime, check)
		bridgegrpc.Register(server, logs, runtime, check)
	}
}
