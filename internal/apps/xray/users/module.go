// Package users composes implemented Xray user capabilities.
package users

import (
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

func Module(config []settings.Instance, requests settings.Requests) fx.Option {
	return fx.Module("xray-users", accounts.Module(requests), online.Module(requests), sync.Module(config, requests))
}
