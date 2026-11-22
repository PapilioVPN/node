// Package users composes implemented Xray user capabilities.
package users

import (
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("xray-users", accounts.Module(requests))
}
