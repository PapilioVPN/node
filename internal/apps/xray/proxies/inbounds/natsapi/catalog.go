package natsapi

import (
	"context"
	"encoding/json"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/infra/jsonwire"
	"github.com/nats-io/nats.go"
)

func (h Handler) Summaries(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, r, err := h.request(ctx, data, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	if r.Tag != nil || r.HasConfiguration || r.HasSources || r.HasReplacement() {
		return failure(domain.ErrInvalidArgument)
	}
	result, err := h.Catalog.List(ctx, *r.InstanceID, r.Page())
	if err != nil {
		return failure(err)
	}
	encoded, err := jsonwire.EncodeSummaries(result)
	return response(encoded, err)
}
func (h Handler) Compatibility(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, r, err := h.request(ctx, data, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	if r.Tag == nil || r.HasConfiguration || r.HasSources || r.HasReplacement() || r.HasPage() {
		return failure(domain.ErrInvalidArgument)
	}
	result, err := h.Catalog.Compatibility(ctx, *r.InstanceID, *r.Tag)
	if err != nil {
		return failure(err)
	}
	encoded, err := jsonwire.EncodeCompatibility(result)
	return response(encoded, err)
}
func (h Handler) Plan(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, r, err := h.request(ctx, data, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	if r.Tag == nil || !r.HasConfiguration || r.HasSources || r.HasPage() || r.Token != nil {
		return failure(domain.ErrInvalidArgument)
	}
	input := domain.ReplacementInput{Tag: *r.Tag, Configuration: r.Configuration}
	if r.AllowInterruption != nil {
		input.AllowInterruption = *r.AllowInterruption
	}
	if r.DisconnectExisting != nil {
		input.DisconnectExisting = *r.DisconnectExisting
	}
	result, err := h.Catalog.Plan(ctx, *r.InstanceID, input)
	if err != nil {
		return failure(err)
	}
	encoded, err := json.Marshal(jsonwire.PlanResult(result))
	return response(encoded, err)
}
func (h Handler) Replace(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, r, err := h.request(ctx, data, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	if r.Tag != nil || r.HasConfiguration || r.HasSources || r.HasPage() || r.Token == nil || r.AllowInterruption != nil || r.DisconnectExisting != nil {
		return failure(domain.ErrInvalidArgument)
	}
	result, err := h.Catalog.Replace(ctx, *r.InstanceID, *r.Token)
	return mutation(result, err)
}
