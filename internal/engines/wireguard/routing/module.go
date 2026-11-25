// Package routing owns the optional Linux forwarding rules for WireGuard.
package routing

import (
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

var Module = fx.Module("wireguard-routing", fx.Provide(newRouting))

func newRouting(config settings.WireGuard, lifecycle fx.Lifecycle) *Service {
	service := &Service{options: config.Routing}
	if config.Routing != nil {
		lifecycle.Append(fx.Hook{OnStart: service.open, OnStop: service.close})
	}
	return service
}
