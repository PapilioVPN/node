// Package users composes independent account and presence capabilities.
package users

import (
	"go.uber.org/fx"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online"
)

func Module(grpcEnabled bool) fx.Option {
	return fx.Module("users", accounts.Module(grpcEnabled), online.Module(grpcEnabled))
}
