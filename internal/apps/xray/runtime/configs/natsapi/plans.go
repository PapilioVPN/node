package natsapi

import (
	"context"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/infra/jsonwire"
	"github.com/nats-io/nats.go"
)

func (h Handler) Plan(ctx context.Context, data []byte, headers nats.Header) []byte {
	input, err := jsonwire.DecodeState(data)
	if err != nil {
		return failure(err)
	}
	if input.InstanceID == nil || input.ExpectedRevision == nil || !input.HasSources {
		return failure(domain.ErrInvalidArgument)
	}
	if h.Plans == nil {
		return failure(domain.ErrUnsupported)
	}
	ctx, cancel, err := h.requestContext(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Plans.Plan(ctx, *input.InstanceID, *input.ExpectedRevision, input.Sources)
	if err != nil {
		return failure(err)
	}
	response, err := jsonwire.EncodePlan(result)
	if err != nil {
		return failure(err)
	}
	return response
}
func (h Handler) ApplyPlan(ctx context.Context, data []byte, headers nats.Header) []byte {
	input, err := jsonwire.DecodeIntent(data, "instance_id", "plan_id")
	if err != nil {
		return failure(err)
	}
	if len(input) != 2 {
		return failure(domain.ErrInvalidArgument)
	}
	if h.Plans == nil {
		return failure(domain.ErrUnsupported)
	}
	ctx, cancel, err := h.requestContext(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Plans.Apply(ctx, input["instance_id"], input["plan_id"])
	if result.ID == "" && err != nil {
		return failure(err)
	}
	response, err := jsonwire.EncodeApplication(result)
	if err != nil {
		return failure(err)
	}
	return response
}
func (h Handler) Application(ctx context.Context, data []byte, headers nats.Header) []byte {
	input, err := jsonwire.DecodeIntent(data, "instance_id", "application_id")
	if err != nil {
		return failure(err)
	}
	if len(input) != 2 {
		return failure(domain.ErrInvalidArgument)
	}
	if h.Plans == nil {
		return failure(domain.ErrUnsupported)
	}
	ctx, cancel, err := h.requestContext(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Plans.Application(ctx, input["instance_id"], input["application_id"])
	if err != nil {
		return failure(err)
	}
	response, err := jsonwire.EncodeApplication(result)
	if err != nil {
		return failure(err)
	}
	return response
}
func (h Handler) Recover(ctx context.Context, data []byte, headers nats.Header) []byte {
	input, err := jsonwire.DecodeIntent(data, "instance_id", "application_id", "expected_revision", "recovery_id")
	if err != nil {
		return failure(err)
	}
	if len(input) != 4 {
		return failure(domain.ErrInvalidArgument)
	}
	revision, err := jsonwire.Revision(input)
	if err != nil {
		return failure(err)
	}
	if h.Plans == nil {
		return failure(domain.ErrUnsupported)
	}
	ctx, cancel, err := h.requestContext(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Plans.Recover(ctx, input["instance_id"], input["application_id"], revision, input["recovery_id"])
	if result.ID == "" && err != nil {
		return failure(err)
	}
	response, err := jsonwire.EncodeApplication(result)
	if err != nil {
		return failure(err)
	}
	return response
}
