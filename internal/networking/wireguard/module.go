// Package wireguard owns optional Linux forwarding rules for WireGuard.
package wireguard

import (
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

var Module = fx.Module("wireguard-networking", fx.Provide(newRouting))

func newRouting(config settings.WireGuard, lifecycle fx.Lifecycle) *Service {
	service := &Service{options: config.Routing}
	if config.Routing != nil {
		lifecycle.Append(fx.Hook{OnStart: service.open, OnStop: service.close})
	}
	return service
}
