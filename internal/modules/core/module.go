// Package core composes engine-independent Node capabilities.
package core

import (
	"go.uber.org/fx"

	"github.com/PapilioVPN/node/internal/modules/core/instances"
	"github.com/PapilioVPN/node/internal/modules/core/users"
)

func Module(grpcEnabled bool) fx.Option {
	return fx.Module("core", instances.Module(grpcEnabled), users.Module(grpcEnabled))
}
