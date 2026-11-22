package natsapi

import (
	"context"

	sourcewire "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/infra/jsonwire"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/jsonwire"
	"github.com/nats-io/nats.go"
)

func (h Handler) PrepareConfiguration(ctx context.Context, data []byte, headers nats.Header) []byte {
	input, err := jsonwire.DecodePreparation(data)
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
	result, err := h.Configurations.Prepare(ctx, *input.InstanceID, input.Sources, input.Inbounds)
	if err != nil {
		return failure(err)
	}
	data, err = sourcewire.EncodeCompilation(result)
	return response(data, err)
}
