// Package system composes node and host observation capabilities.
package system

import (
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"

	"github.com/PapilioVPN/node/internal/modules/system/info"
)

func Module(version string, communications settings.Communications) fx.Option {
	return fx.Module("system", info.Module(version, communications))
}
