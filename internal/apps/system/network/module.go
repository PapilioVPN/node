// Package network composes implemented network capabilities.
package network

import (
	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("network", interfaces.Module(requests))
}
