package natsapi

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/infra/jsonwire"
	"github.com/nats-io/nats.go"
)

func (h Handler) Get(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, err := h.requestContext(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	input, err := jsonwire.DecodeState(data)
	if err != nil {
		return failure(err)
	}
	if input.InstanceID == nil || input.ExpectedRevision != nil || input.HasSources {
		return failure(domain.ErrInvalidArgument)
	}
	if h.State == nil {
		return failure(domain.ErrUnsupported)
	}
	result, err := h.State.Read(ctx, *input.InstanceID)
	if err != nil {
		return failure(err)
	}
	response, err := jsonwire.EncodeDesired(*input.InstanceID, result)
	if err != nil {
		return failure(err)
	}
	return response
}
func (h Handler) Metadata(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, err := h.requestContext(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	input, err := jsonwire.DecodeState(data)
	if err != nil {
		return failure(err)
	}
	if input.InstanceID == nil || input.ExpectedRevision != nil || input.HasSources {
		return failure(domain.ErrInvalidArgument)
	}
	if h.State == nil {
		return failure(domain.ErrUnsupported)
	}
	result, err := h.State.Metadata(ctx, *input.InstanceID)
	if err != nil {
		return failure(err)
	}
	response, err := jsonwire.EncodeMetadata(*input.InstanceID, result)
	if err != nil {
		return failure(err)
	}
	return response
}
func (h Handler) Replace(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, err := h.requestContext(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	input, err := jsonwire.DecodeState(data)
	if err != nil {
		return failure(err)
	}
	if input.InstanceID == nil || input.ExpectedRevision == nil || !input.HasSources {
		return failure(domain.ErrInvalidArgument)
	}
	if h.State == nil {
		return failure(domain.ErrUnsupported)
	}
	result, err := h.State.Replace(ctx, *input.InstanceID, *input.ExpectedRevision, input.Sources)
	return changeResult(*input.InstanceID, result, err)
}
func (h Handler) Apply(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, err := h.requestContext(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	input, err := jsonwire.DecodeState(data)
	if err != nil {
		return failure(err)
	}
	if input.InstanceID == nil || input.ExpectedRevision == nil || input.HasSources {
		return failure(domain.ErrInvalidArgument)
	}
	if h.State == nil {
		return failure(domain.ErrUnsupported)
	}
	result, err := h.State.Apply(ctx, *input.InstanceID, *input.ExpectedRevision)
	return changeResult(*input.InstanceID, result, err)
}
func changeResult(id string, result domain.Change, err error) []byte {
	code := ""
	if err != nil {
		code = errorCode(err)
	}
	response, encodeErr := jsonwire.EncodeChange(id, result, code)
	if encodeErr != nil {
		return failure(encodeErr)
	}
	return response
}
