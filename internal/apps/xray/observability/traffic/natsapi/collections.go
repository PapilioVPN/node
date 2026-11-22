package natsapi

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/infra/jsonwire"
	"github.com/nats-io/nats.go"
)

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
func (h Handler) List(ctx context.Context, data []byte, headers nats.Header) []byte {
	id, request, err := jsonwire.DecodePage(data)
	if err != nil {
		return failure(err)
	}
	ctx, cancel, err := h.deadline(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.List(ctx, id, request)
	if err != nil {
		return failure(err)
	}
	data, err = jsonwire.EncodePage(result)
	if err != nil {
		return failure(err)
	}
	return data
}
func (h Handler) Pair(ctx context.Context, data []byte, headers nats.Header) []byte {
	id, scope, name, err := jsonwire.DecodePair(data)
	if err != nil {
		return failure(err)
	}
	ctx, cancel, err := h.deadline(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.ReadPair(ctx, id, scope, name)
	if err != nil {
		return failure(err)
	}
	data, err = jsonwire.EncodePair(result)
	if err != nil {
		return failure(err)
	}
	return data
}
func (h Handler) ResetCounter(ctx context.Context, data []byte, headers nats.Header) []byte {
	return h.reset(ctx, data, headers, domain.ResetExact)
}
func (h Handler) ResetPair(ctx context.Context, data []byte, headers nats.Header) []byte {
	return h.reset(ctx, data, headers, domain.ResetPair)
}
func (h Handler) ResetScope(ctx context.Context, data []byte, headers nats.Header) []byte {
	return h.reset(ctx, data, headers, domain.ResetScope)
}
func (h Handler) reset(ctx context.Context, data []byte, headers nats.Header, kind domain.ResetKind) []byte {
	id, request, err := jsonwire.DecodeReset(data, kind)
	if err != nil {
		return failure(err)
	}
	ctx, cancel, err := h.deadline(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.Reset(ctx, id, request)
	if err != nil {
		return failure(err)
	}
	data, err = jsonwire.EncodeReset(result)
	if err != nil {
		return failure(err)
	}
	return data
}
func (h Handler) ResetStatus(ctx context.Context, data []byte, headers nats.Header) []byte {
	id, revision, offset, err := jsonwire.DecodeStatus(data)
	if err != nil {
		return failure(err)
	}
	ctx, cancel, err := h.deadline(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.ResetStatus(ctx, id, revision, offset)
	if err != nil {
		return failure(err)
	}
	data, err = jsonwire.EncodeReset(result)
	if err != nil {
		return failure(err)
	}
	return data
}
