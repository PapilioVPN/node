// Package system composes node and host observation capabilities.
package system

import (
	"go.uber.org/fx"

	"github.com/PapilioVPN/node/internal/modules/system/info"
)

func Module(version string, grpcEnabled bool) fx.Option {
	return fx.Module("system", info.Module(version, grpcEnabled))
}
