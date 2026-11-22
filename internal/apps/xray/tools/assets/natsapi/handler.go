// Package natsapi adapts asset operations under the existing broker ACL boundary.
package natsapi

import (
	"context"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/infra/jsonwire"
	"github.com/nats-io/nats.go"
	"time"
)

type Handler struct {
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) deadline(ctx context.Context, headers nats.Header) (context.Context, context.CancelFunc, error) {
	deadline := time.Now().Add(h.Timeout)
	if values := headers.Values("Papilio-Deadline"); len(values) > 0 {
		if len(values) != 1 {
			return nil, nil, domain.ErrInvalidArgument
		}
		value, err := time.Parse(time.RFC3339Nano, values[0])
		if err != nil {
			return nil, nil, domain.ErrInvalidArgument
		}
		if value.Before(deadline) {
			deadline = value
		}
	}
	child, cancel := context.WithDeadline(ctx, deadline)
	return child, cancel, nil
}
func (h Handler) Inspect(ctx context.Context, data []byte, headers nats.Header) []byte {
	id, err := jsonwire.Inspect(data, "")
	if err != nil {
		return jsonwire.Failure(err)
	}
	ctx, cancel, err := h.deadline(ctx, headers)
	if err != nil {
		return jsonwire.Failure(err)
	}
	defer cancel()
	result, err := h.Service.Inspect(ctx, id)
	if err != nil {
		return jsonwire.Failure(err)
	}
	return jsonwire.Encode(result)
}
func (h Handler) Stage(ctx context.Context, data []byte, headers nats.Header) []byte {
	id, q, err := jsonwire.Stage(data, "")
	if err != nil {
		return jsonwire.Failure(err)
	}
	ctx, cancel, err := h.deadline(ctx, headers)
	if err != nil {
		return jsonwire.Failure(err)
	}
	defer cancel()
	result, err := h.Service.Stage(ctx, id, q)
	if err != nil {
		return jsonwire.Failure(err)
	}
	return jsonwire.Encode(result)
}
func (h Handler) Activate(ctx context.Context, data []byte, headers nats.Header) []byte {
	id, q, err := jsonwire.Activation(data, "", true)
	if err != nil {
		return jsonwire.Failure(err)
	}
	ctx, cancel, err := h.deadline(ctx, headers)
	if err != nil {
		return jsonwire.Failure(err)
	}
	defer cancel()
	result, err := h.Service.Activate(ctx, id, q)
	return jsonwire.Encode(jsonwire.ActivationResult{Receipt: result, ErrorCode: jsonwire.ErrorCode(err)})
}
func (h Handler) Rollback(ctx context.Context, data []byte, headers nats.Header) []byte {
	id, q, err := jsonwire.Activation(data, "", false)
	if err != nil {
		return jsonwire.Failure(err)
	}
	ctx, cancel, err := h.deadline(ctx, headers)
	if err != nil {
		return jsonwire.Failure(err)
	}
	defer cancel()
	result, err := h.Service.Rollback(ctx, id, q)
	return jsonwire.Encode(jsonwire.ActivationResult{Receipt: result, ErrorCode: jsonwire.ErrorCode(err)})
}
func (h Handler) Recover(ctx context.Context, data []byte, headers nats.Header) []byte {
	id, q, err := jsonwire.Activation(data, "", false)
	if err != nil {
		return jsonwire.Failure(err)
	}
	ctx, cancel, err := h.deadline(ctx, headers)
	if err != nil {
		return jsonwire.Failure(err)
	}
	defer cancel()
	result, err := h.Service.Recover(ctx, id, q)
	return jsonwire.Encode(jsonwire.ActivationResult{Receipt: result, ErrorCode: jsonwire.ErrorCode(err)})
}
