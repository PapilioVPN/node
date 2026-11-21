package natsapi

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/infra/jsonwire"
	"github.com/nats-io/nats.go"
)

func (h Handler) Compile(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, err := h.requestContext(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	id, sources, err := jsonwire.DecodeCompilation(data)
	if err != nil {
		return failure(err)
	}
	result, err := h.Service.Compile(ctx, id, sources)
	if err != nil {
		return failure(err)
	}
	response, err := jsonwire.EncodeCompilation(result)
	if err != nil {
		return failure(err)
	}
	return response
}
