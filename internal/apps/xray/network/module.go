// Package network composes Xray networking capabilities.
package network

import (
	"github.com/PapilioVPN/node/internal/apps/xray/network/routing"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("xray-network", routing.Module(requests))
}
