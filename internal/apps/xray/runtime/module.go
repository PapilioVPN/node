// Package runtime composes engine configuration and execution observations.
package runtime

import (
	"go.uber.org/fx"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/info"
)

func Module(grpcEnabled bool) fx.Option {
	return fx.Module("runtime", configs.Module(), info.Module(grpcEnabled))
}
