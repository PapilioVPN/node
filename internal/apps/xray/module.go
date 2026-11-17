// Package xray composes Xray's implemented responsibility groups.
package xray

import (
	"go.uber.org/fx"

	"github.com/PapilioVPN/node/internal/apps/xray/network"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime"
	"github.com/PapilioVPN/node/internal/apps/xray/users"
)

func Module(grpcEnabled bool) fx.Option {
	return fx.Module("xray", runtime.Module(grpcEnabled), users.Module(grpcEnabled), network.Module(grpcEnabled))
}
