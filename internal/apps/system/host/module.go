// Package host composes implemented host capabilities.
package host

import (
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu"
	"github.com/PapilioVPN/node/internal/apps/system/host/identity"
	"github.com/PapilioVPN/node/internal/apps/system/host/memory"
	"github.com/PapilioVPN/node/internal/apps/system/host/processes"
	"github.com/PapilioVPN/node/internal/apps/system/host/sessions"
	"github.com/PapilioVPN/node/internal/apps/system/host/uptime"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

func Module(requests settings.Requests, sources settings.Sessions) fx.Option {
	return fx.Module("host", identity.Module(requests), uptime.Module(requests), cpu.Module(requests), memory.Module(requests), processes.Module(requests), sessions.Module(requests, sources))
}
