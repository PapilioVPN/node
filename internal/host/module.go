// Package host exposes observations from the local operating system.
package host

import (
	"github.com/PapilioVPN/node/internal/host/bridge"
	"go.uber.org/fx"
)

var Module = fx.Module("host", fx.Invoke(bridge.Register))
