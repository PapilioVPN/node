// Package runtime composes Xray runtime capabilities.
package runtime

import (
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/statistics"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

func Module(config []settings.Instance, requests settings.Requests) fx.Option {
	return fx.Module("xray-runtime", configs.Module(config, requests), instances.Module(config, requests), statistics.Module(requests))
}
