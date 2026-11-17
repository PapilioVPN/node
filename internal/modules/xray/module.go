// Package xray composes the implemented Xray capability modules.
package xray

import (
	"go.uber.org/fx"

	"github.com/PapilioVPN/node/internal/modules/xray/configs"
	"github.com/PapilioVPN/node/internal/modules/xray/info"
	"github.com/PapilioVPN/node/internal/modules/xray/online"
	"github.com/PapilioVPN/node/internal/modules/xray/outbounds"
	"github.com/PapilioVPN/node/internal/modules/xray/routing"
	"github.com/PapilioVPN/node/internal/modules/xray/traffic"
	"github.com/PapilioVPN/node/internal/modules/xray/users"
)

func Module(grpcEnabled bool) fx.Option {
	return fx.Module("xray",
		configs.Module(),
		info.Module(grpcEnabled),
		users.Module(grpcEnabled),
		online.Module(grpcEnabled),
		routing.Module(grpcEnabled),
		traffic.Module(grpcEnabled),
		outbounds.Module(grpcEnabled),
	)
}
