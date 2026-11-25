// Package wireguard composes Node's Linux WireGuard capabilities.
package wireguard

import (
	"github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces"
	"github.com/PapilioVPN/node/internal/engines/wireguard/routing"
	"github.com/PapilioVPN/node/internal/engines/wireguard/stats"
	"go.uber.org/fx"
)

var Module = fx.Module("wireguard", interfaces.Module, stats.Module, diagnostics.Module, routing.Module)
