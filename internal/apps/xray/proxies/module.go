// Package proxies composes implemented Xray proxy capabilities.
package proxies

import (
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("xray-proxies", inbounds.Module(requests))
}
