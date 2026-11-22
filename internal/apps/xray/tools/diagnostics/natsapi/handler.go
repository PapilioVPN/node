// Package natsapi adapts broker-authorized diagnostic requests.
package natsapi

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/infra/jsonwire"
	"github.com/nats-io/nats.go"
)

type Handler struct {
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Report(ctx context.Context, data []byte, headers nats.Header) []byte {
	input, err := jsonwire.Decode(data)
	if err != nil {
		return jsonwire.Failure(err)
	}
	deadline := time.Now().Add(h.Timeout)
	if values, ok := headers["Papilio-Deadline"]; ok {
		if len(values) != 1 {
			return jsonwire.Failure(domain.ErrInvalidArgument)
		}
		requested, err := time.Parse(time.RFC3339Nano, values[0])
		if err != nil {
			return jsonwire.Failure(domain.ErrInvalidArgument)
		}
		if requested.Before(deadline) {
			deadline = requested
		}
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	report, err := h.Service.Report(ctx, input)
	if err != nil {
		return jsonwire.Failure(err)
	}
	result, err := jsonwire.Encode(report)
	if err != nil {
		return jsonwire.Failure(err)
	}
	return result
}
