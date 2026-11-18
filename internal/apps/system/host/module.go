// Package host composes implemented host capabilities.
package host

import (
	"github.com/PapilioVPN/node/internal/apps/system/host/identity"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

func Module(config settings.Host, requests settings.Requests) fx.Option {
	if config.Identity == nil {
		return fx.Module("host")
	}
	return fx.Module("host", identity.Module(*config.Identity, requests))
}
