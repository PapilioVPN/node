// Package runtime composes Xray runtime capabilities.
package runtime

import (
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

func Module(config []settings.Instance, requests settings.Requests) fx.Option {
	return fx.Module("xray-runtime", instances.Module(config, requests), configs.Module(config, requests))
}
