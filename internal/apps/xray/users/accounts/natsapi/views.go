package natsapi

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/jsonwire"
	"github.com/nats-io/nats.go"
)

func viewResponse(data []byte, err error) []byte {
	if err != nil {
		return failure(err)
	}
	return data
}
func (h Handler) ListMetadata(ctx context.Context, data []byte, headers nats.Header) []byte {
	input, err := jsonwire.DecodeSelection(data)
	if err != nil {
		return failure(err)
	}
	if input.Email != nil || input.AccountType != nil {
		return failure(domain.ErrInvalidArgument)
	}
	ctx, cancel, err := h.requestContext(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.ListMetadata(ctx, input.InstanceID, input.InboundTag, input.Page())
	if err != nil {
		return failure(err)
	}
	encoded, err := jsonwire.EncodeMetadataPage(result)
	return viewResponse(encoded, err)
}
func (h Handler) Export(ctx context.Context, data []byte, headers nats.Header) []byte {
	input, err := jsonwire.DecodeSelection(data)
	if err != nil {
		return failure(err)
	}
	if input.Email != nil || input.AccountType != nil {
		return failure(domain.ErrInvalidArgument)
	}
	ctx, cancel, err := h.requestContext(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.Export(ctx, input.InstanceID, input.InboundTag, input.Page())
	if err != nil {
		return failure(err)
	}
	encoded, err := jsonwire.EncodeExport(result)
	return viewResponse(encoded, err)
}
func (h Handler) GetMetadata(ctx context.Context, data []byte, headers nats.Header) []byte {
	input, err := jsonwire.DecodeSelection(data)
	if err != nil {
		return failure(err)
	}
	if input.Email == nil || input.AccountType != nil || input.HasPage() {
		return failure(domain.ErrInvalidArgument)
	}
	ctx, cancel, err := h.requestContext(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.GetMetadata(ctx, input.InstanceID, input.InboundTag, *input.Email)
	if err != nil {
		return failure(err)
	}
	encoded, err := jsonwire.EncodeMetadata(result)
	return viewResponse(encoded, err)
}
func (h Handler) Compatibility(ctx context.Context, data []byte, headers nats.Header) []byte {
	input, err := jsonwire.DecodeSelection(data)
	if err != nil {
		return failure(err)
	}
	if input.Email != nil || input.HasPage() {
		return failure(domain.ErrInvalidArgument)
	}
	ctx, cancel, err := h.requestContext(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.CheckCompatibility(ctx, input.InstanceID, input.InboundTag, input.AccountType)
	if err != nil {
		return failure(err)
	}
	encoded, err := jsonwire.EncodeCompatibility(result)
	return viewResponse(encoded, err)
}
