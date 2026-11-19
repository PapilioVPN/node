// Package node composes capabilities that observe Node itself.
package node

import (
	"github.com/PapilioVPN/node/internal/apps/system/node/runtime"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

func Module(requests settings.Requests) fx.Option { return fx.Module("node", runtime.Module(requests)) }
