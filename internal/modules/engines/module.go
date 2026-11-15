// Package engines composes the implemented engine capability modules.
package engines

import (
	"go.uber.org/fx"

	"github.com/PapilioVPN/node/internal/modules/engines/xray"
)

func Module(grpcEnabled bool) fx.Option {
	return fx.Module("engines", xray.Module(grpcEnabled))
}
