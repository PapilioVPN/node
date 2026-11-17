// Package engines composes engine-independent instance management.
package engines

import (
	"go.uber.org/fx"

	"github.com/PapilioVPN/node/internal/apps/core/engines/instances"
)

func Module(grpcEnabled bool) fx.Option {
	return fx.Module("engines", instances.Module(grpcEnabled))
}
