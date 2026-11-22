package natsapi

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/infra/jsonwire"
	"github.com/nats-io/nats.go"
)

func (h Handler) controlRequest(ctx context.Context, data []byte, headers nats.Header) (context.Context, context.CancelFunc, jsonwire.Request, error) {
	input, err := jsonwire.DecodeControl(data)
	if err != nil {
		return nil, nil, input, err
	}
	if input.InstanceID == nil {
		return nil, nil, input, domain.ErrInvalidArgument
	}
	ctx, cancel, err := h.requestContext(ctx, headers)
	return ctx, cancel, input, err
}
func (h Handler) Status(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, input, err := h.controlRequest(ctx, data, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	if input.Tag != nil || input.HasConfiguration || input.HasSources {
		return failure(domain.ErrInvalidArgument)
	}
	result, err := h.Service.Status(ctx, *input.InstanceID)
	if err != nil {
		return failure(err)
	}
	encoded, err := jsonwire.EncodeStatus(result)
	return response(encoded, err)
}
func (h Handler) Add(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, input, err := h.controlRequest(ctx, data, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	if input.Tag != nil || !input.HasConfiguration || input.HasSources {
		return failure(domain.ErrInvalidArgument)
	}
	result, err := h.Service.Add(ctx, *input.InstanceID, input.Configuration)
	return mutation(result, err)
}
func (h Handler) Remove(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, input, err := h.controlRequest(ctx, data, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	if input.Tag == nil || input.HasConfiguration || input.HasSources {
		return failure(domain.ErrInvalidArgument)
	}
	result, err := h.Service.Remove(ctx, *input.InstanceID, *input.Tag)
	return mutation(result, err)
}
func mutation(result domain.Operation, err error) []byte {
	code := ""
	if err != nil {
		code = errorCode(err)
	}
	data, err := jsonwire.EncodeOperation(result, code)
	return response(data, err)
}

func (h Handler) AddSources(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, input, err := h.controlRequest(ctx, data, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	if input.Tag == nil || !input.HasSources || input.HasConfiguration {
		return failure(domain.ErrInvalidArgument)
	}
	result, err := h.Sources.Add(ctx, *input.InstanceID, *input.Tag, input.Sources)
	return mutation(result, err)
}

func response(data []byte, err error) []byte {
	if err != nil {
		return failure(err)
	}
	return data
}
