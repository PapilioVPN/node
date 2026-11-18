// Package system composes implemented system capability groups.
package system

import (
	"github.com/PapilioVPN/node/internal/apps/system/host"
	"github.com/PapilioVPN/node/internal/apps/system/network"
	"github.com/PapilioVPN/node/internal/apps/system/storage"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("system", host.Module(requests), network.Module(requests), storage.Module(requests))
}
