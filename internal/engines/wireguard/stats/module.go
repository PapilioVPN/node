// Package stats composes the WireGuard traffic observations and their APIs.
package stats

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/native"
	"github.com/PapilioVPN/node/internal/engines/wireguard/stats/accounting"
	bridgegrpc "github.com/PapilioVPN/node/internal/engines/wireguard/stats/bridge/grpc"
	bridgehttp "github.com/PapilioVPN/node/internal/engines/wireguard/stats/bridge/http"
	"github.com/PapilioVPN/node/internal/engines/wireguard/stats/traffic"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

var Module = fx.Module("wireguard-stats", fx.Provide(newTraffic, newAccounting), fx.Invoke(register))

func newTraffic(lifecycle fx.Lifecycle, config settings.WireGuard) *traffic.Service {
	links := new(native.Reader)
	peers := new(native.PeerStatsReader)
	if config.Enabled() {
		lifecycle.Append(fx.Hook{OnStart: links.Open, OnStop: links.Close})
		lifecycle.Append(fx.Hook{OnStop: peers.Close})
	}
	return traffic.New(config.Interface, links, peers, config.Managed)
}

func newAccounting(lifecycle fx.Lifecycle, config settings.WireGuard) *accounting.Service {
	reader := new(native.PeerStatsReader)
	service := accounting.New(config.Interface, reader, config.Managed, config.PeerRemovalUsage == settings.WireGuardSampledPeerRemoval)
	if config.Managed {
		lifecycle.Append(fx.Hook{OnStart: service.Start, OnStop: func(ctx context.Context) error {
			return errors.Join(service.Stop(ctx), reader.Close(ctx))
		}})
	}
	return service
}

func register(config settings.WireGuard, router *chi.Mux, server *grpc.Server, service *traffic.Service, usage *accounting.Service) {
	if !config.Enabled() {
		return
	}
	bridgehttp.Register(router, service, usage)
	bridgegrpc.Register(server, service, usage)
}
