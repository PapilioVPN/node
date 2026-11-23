// Package host exposes observations from the local operating system.
package host

import (
	"github.com/PapilioVPN/node/internal/host/bridge"
	"github.com/PapilioVPN/node/internal/host/services"
	"go.uber.org/fx"
)

var Module = fx.Module("host",
	fx.Provide(services.NewCPUSampler),
	fx.Invoke(registerCPUSampler, bridge.Register),
)

func registerCPUSampler(lifecycle fx.Lifecycle, sampler *services.CPUSampler) {
	lifecycle.Append(fx.Hook{OnStart: sampler.Start, OnStop: sampler.Stop})
}
