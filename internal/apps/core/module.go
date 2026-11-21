// Package core composes engine-independent Node capabilities.
package core

import (
	"github.com/PapilioVPN/node/internal/apps/core/engines"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

func Module(config []settings.Instance, requests settings.Requests) fx.Option {
	return fx.Module("core", engines.Module(config, requests))
}
