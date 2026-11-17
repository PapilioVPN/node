// Package network composes native routing, traffic and outbound capabilities.
package network

import (
	"go.uber.org/fx"

	"github.com/PapilioVPN/node/internal/apps/xray/network/outbounds"
	"github.com/PapilioVPN/node/internal/apps/xray/network/routing"
	"github.com/PapilioVPN/node/internal/apps/xray/network/traffic"
)

func Module(grpcEnabled bool) fx.Option {
	return fx.Module("network", routing.Module(grpcEnabled), traffic.Module(grpcEnabled), outbounds.Module(grpcEnabled))
}
