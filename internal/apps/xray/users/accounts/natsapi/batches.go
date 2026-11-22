package natsapi

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/jsonwire"
	"github.com/nats-io/nats.go"
)

func (h Handler) AddMany(ctx context.Context, data []byte, headers nats.Header) []byte {
	input, err := jsonwire.DecodeAdditions(data)
	if err != nil {
		return failure(err)
	}
	if input.InstanceID == nil {
		return failure(domain.ErrInvalidArgument)
	}
	ctx, cancel, err := h.requestContext(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	if len(input.Sources) != 0 {
		result, err := h.Sources.AddMany(ctx, *input.InstanceID, input.Selections, input.Sources, input.Parallelism)
		return batchResult(result, err)
	}
	result, err := h.Service.AddMany(ctx, *input.InstanceID, input.Items, input.Parallelism)
	return batchResult(result, err)
}
func (h Handler) RemoveMany(ctx context.Context, data []byte, headers nats.Header) []byte {
	input, err := jsonwire.DecodeRemovals(data)
	if err != nil {
		return failure(err)
	}
	if input.InstanceID == nil {
		return failure(domain.ErrInvalidArgument)
	}
	ctx, cancel, err := h.requestContext(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.RemoveMany(ctx, *input.InstanceID, input.Items, input.Parallelism)
	return batchResult(result, err)
}
func batchResult(result domain.Batch, cause error) []byte {
	if len(result.Items) == 0 && cause != nil {
		return failure(cause)
	}
	codes := make([]string, len(result.Items))
	for i, item := range result.Items {
		if item.Err != nil {
			codes[i] = errorCode(item.Err)
		}
	}
	code := ""
	if cause != nil {
		code = errorCode(cause)
	}
	data, err := jsonwire.EncodeBatch(result, codes, code)
	return response(data, err)
}
