// Package natsapi adapts broker-authorized requests to outbound health reads.
package natsapi

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/infra/jsonwire"
	"github.com/nats-io/nats.go"
)

type Handler struct {
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Get(ctx context.Context, data []byte, headers nats.Header) []byte {
	input, err := jsonwire.Decode(data, false)
	if err != nil {
		return jsonwire.Failure(err)
	}
	ctx, cancel, err := h.deadline(ctx, headers)
	if err != nil {
		return jsonwire.Failure(err)
	}
	defer cancel()
	result, err := h.Service.Get(ctx, input.InstanceID, domain.Selection{Tag: *input.Tag, MaxSampleAgeSeconds: input.Page.MaxSampleAgeSeconds})
	if err != nil {
		return jsonwire.Failure(err)
	}
	encoded, err := jsonwire.EncodeObservation(result)
	if err != nil {
		return jsonwire.Failure(err)
	}
	return encoded
}
func (h Handler) List(ctx context.Context, data []byte, headers nats.Header) []byte {
	input, err := jsonwire.Decode(data, true)
	if err != nil {
		return jsonwire.Failure(err)
	}
	ctx, cancel, err := h.deadline(ctx, headers)
	if err != nil {
		return jsonwire.Failure(err)
	}
	defer cancel()
	result, err := h.Service.List(ctx, input.InstanceID, input.Page)
	if err != nil {
		return jsonwire.Failure(err)
	}
	encoded, err := jsonwire.EncodePage(result)
	if err != nil {
		return jsonwire.Failure(err)
	}
	return encoded
}
func (h Handler) deadline(ctx context.Context, headers nats.Header) (context.Context, context.CancelFunc, error) {
	deadline := time.Now().Add(h.Timeout)
	if values, ok := headers["Papilio-Deadline"]; ok {
		if len(values) != 1 {
			return nil, nil, domain.ErrInvalidArgument
		}
		requested, err := time.Parse(time.RFC3339Nano, values[0])
		if err != nil {
			return nil, nil, domain.ErrInvalidArgument
		}
		if requested.Before(deadline) {
			deadline = requested
		}
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	return ctx, cancel, nil
}
