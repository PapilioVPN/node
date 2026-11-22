package natsapi

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/infra/jsonwire"
	"github.com/nats-io/nats.go"
)

func (h Handler) PlanReplacement(ctx context.Context, data []byte, headers nats.Header) []byte {
	input, err := jsonwire.DecodePlan(data)
	if err != nil {
		return failure(err)
	}
	if input.Control.InstanceID == nil {
		return failure(domain.ErrInvalidArgument)
	}
	ctx, cancel, err := h.requestContext(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	var plan domain.ReplacementPlan
	if input.Control.HasSources {
		plan, err = h.Replacements.PlanSources(ctx, *input.Control.InstanceID, *input.Control.Tag, input.Control.Sources, input.DefaultPolicy, input.ConnectionPolicy)
	} else {
		plan, err = h.Replacements.Plan(ctx, *input.Control.InstanceID, domain.ReplacementRequest{Tag: *input.Control.Tag, Configuration: input.Control.Configuration, DefaultPolicy: input.DefaultPolicy, ConnectionPolicy: input.ConnectionPolicy})
	}
	if err != nil {
		return failure(err)
	}
	data, err = jsonwire.EncodePlan(plan)
	return response(data, err)
}
func (h Handler) ApplyReplacement(ctx context.Context, data []byte, headers nats.Header) []byte {
	id, plan, err := jsonwire.DecodePlanID(data)
	if err != nil {
		return failure(err)
	}
	if id == nil {
		return failure(domain.ErrInvalidArgument)
	}
	ctx, cancel, err := h.requestContext(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Replacements.Apply(ctx, *id, plan)
	if result.PlanID == "" && err != nil {
		return failure(err)
	}
	data, err = jsonwire.EncodeReplacement(result)
	return response(data, err)
}
func (h Handler) ReplacementResult(ctx context.Context, data []byte, headers nats.Header) []byte {
	id, plan, err := jsonwire.DecodePlanID(data)
	if err != nil {
		return failure(err)
	}
	if id == nil {
		return failure(domain.ErrInvalidArgument)
	}
	ctx, cancel, err := h.requestContext(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Replacements.Result(ctx, *id, plan)
	if err != nil {
		return failure(err)
	}
	data, err = jsonwire.EncodeReplacement(result)
	return response(data, err)
}
