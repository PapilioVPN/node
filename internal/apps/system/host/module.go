// Package host composes implemented host capabilities.
package host

import (
	"github.com/PapilioVPN/node/internal/apps/system/host/identity"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("host", identity.Module(requests))
}
