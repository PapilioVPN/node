// Package xray composes the Xray engine capabilities.
package xray

import (
	"github.com/PapilioVPN/node/internal/engines/xray/configs"
	"github.com/PapilioVPN/node/internal/engines/xray/instances"
	"go.uber.org/fx"
)

var Module = fx.Module("xray", instances.Module, configs.Module)
