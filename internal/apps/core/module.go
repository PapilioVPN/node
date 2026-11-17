// Package core composes engine-independent Node groups.
package core

import (
	"go.uber.org/fx"

	"github.com/PapilioVPN/node/internal/apps/core/engines"
)

func Module(grpcEnabled bool) fx.Option {
	return fx.Module("core", engines.Module(grpcEnabled))
}
