// Package wireguard composes Node's Linux WireGuard capabilities.
package wireguard

import (
	"github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces"
	"github.com/PapilioVPN/node/internal/engines/wireguard/stats"
	wgnet "github.com/PapilioVPN/node/internal/networking/wireguard"
	"go.uber.org/fx"
)

var Module = fx.Module("wireguard", interfaces.Module, stats.Module, diagnostics.Module, wgnet.Module)
