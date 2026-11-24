// Package wireguard composes Node's Linux WireGuard capabilities.
package wireguard

import (
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces"
	"go.uber.org/fx"
)

var Module = fx.Module("wireguard", interfaces.Module)
