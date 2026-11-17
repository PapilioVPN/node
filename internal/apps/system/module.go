// Package system composes node and host observation groups.
package system

import (
	"go.uber.org/fx"

	"github.com/PapilioVPN/node/internal/apps/system/node"
	"github.com/PapilioVPN/node/internal/settings"
)

func Module(version string, communications settings.Communications) fx.Option {
	return fx.Module("system", node.Module(version, communications))
}
