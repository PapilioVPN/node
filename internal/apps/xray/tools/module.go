// Package tools composes Xray's native utility capabilities.
package tools

import (
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

func Module(config []settings.Instance, requests settings.Requests) fx.Option {
	return fx.Module("xray-tools", credentials.Module(config, requests), diagnostics.Module(config, requests))
}
