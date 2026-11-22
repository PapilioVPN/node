// Package xray composes Xray engine capabilities.
package xray

import (
	"github.com/PapilioVPN/node/internal/apps/xray/network"
	"github.com/PapilioVPN/node/internal/apps/xray/observability"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime"
	"github.com/PapilioVPN/node/internal/apps/xray/tools"
	"github.com/PapilioVPN/node/internal/apps/xray/users"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

func Module(config []settings.Instance, requests settings.Requests) fx.Option {
	return fx.Module("xray",
		runtime.Module(config, requests),
		observability.Module(requests),
		proxies.Module(requests),
		users.Module(config, requests),
		network.Module(requests),
		tools.Module(config, requests),
	)
}
