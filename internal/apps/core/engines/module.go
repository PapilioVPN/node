// Package engines composes engine-independent execution capabilities.
package engines

import (
	"github.com/PapilioVPN/node/internal/apps/core/engines/instances"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

func Module(config []settings.Instance, requests settings.Requests) fx.Option {
	return fx.Module("engines", instances.Module(config, requests))
}
