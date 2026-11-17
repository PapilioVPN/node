// Package node composes Node identity and management observations.
package node

import (
	"go.uber.org/fx"

	"github.com/PapilioVPN/node/internal/apps/system/node/info"
	"github.com/PapilioVPN/node/internal/settings"
)

func Module(version string, communications settings.Communications) fx.Option {
	return fx.Module("node", info.Module(version, communications))
}
