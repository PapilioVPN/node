// Package observability composes Xray observation and maintenance capabilities.
package observability

import (
	"github.com/PapilioVPN/node/internal/apps/xray/observability/loggers"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("xray-observability", loggers.Module(requests), traffic.Module(requests), outboundhealth.Module(requests))
}
