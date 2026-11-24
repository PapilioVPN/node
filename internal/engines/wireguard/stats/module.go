// Package stats composes the WireGuard traffic observations and their APIs.
package stats

import (
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/native"
	bridgegrpc "github.com/PapilioVPN/node/internal/engines/wireguard/stats/bridge/grpc"
	bridgehttp "github.com/PapilioVPN/node/internal/engines/wireguard/stats/bridge/http"
	"github.com/PapilioVPN/node/internal/engines/wireguard/stats/traffic"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

var Module = fx.Module("wireguard-stats", fx.Provide(newTraffic), fx.Invoke(register))

func newTraffic(lifecycle fx.Lifecycle, config settings.WireGuard) *traffic.Service {
	links := new(native.Reader)
	peers := new(native.PeerStatsReader)
	if config.Enabled() {
		lifecycle.Append(fx.Hook{OnStart: links.Open, OnStop: links.Close})
		lifecycle.Append(fx.Hook{OnStop: peers.Close})
	}
	return traffic.New(config.Interface, links, peers)
}

func register(config settings.WireGuard, router *chi.Mux, server *grpc.Server, service *traffic.Service) {
	if !config.Enabled() {
		return
	}
	bridgehttp.Register(router, service)
	bridgegrpc.Register(server, service)
}
