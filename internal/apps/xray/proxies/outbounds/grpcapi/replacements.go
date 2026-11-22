package grpcapi

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/grpcapi/proto/v1"
	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (h Handler) PlanOutboundReplacement(ctx context.Context, request *v1.PlanOutboundReplacementRequest) (*v1.OutboundReplacementPlan, error) {
	if request == nil || len(request.Sources) > configs.MaxSources || (len(request.Configuration) > 0) == (len(request.Sources) > 0) {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	var plan domain.ReplacementPlan
	var err error
	if len(request.Sources) > 0 {
		sources := make([]configs.Source, len(request.Sources))
		for i, source := range request.Sources {
			if source == nil {
				return nil, failure(domain.ErrInvalidArgument)
			}
			sources[i] = configs.Source{Name: source.Name, Content: source.Content}
		}
		plan, err = h.Replacements.PlanSources(ctx, request.InstanceId, request.Tag, sources, domain.DefaultPolicy(request.DefaultPolicy), domain.ConnectionPolicy(request.ConnectionPolicy))
	} else {
		plan, err = h.Replacements.Plan(ctx, request.InstanceId, domain.ReplacementRequest{Tag: request.Tag, Configuration: request.Configuration, DefaultPolicy: domain.DefaultPolicy(request.DefaultPolicy), ConnectionPolicy: domain.ConnectionPolicy(request.ConnectionPolicy)})
	}
	if err != nil {
		return nil, failure(err)
	}
	return &v1.OutboundReplacementPlan{PlanId: plan.ID, InstanceId: plan.InstanceID, Generation: plan.Generation, OwnerRevision: plan.OwnerRevision, Tag: plan.Tag, Mode: plan.Mode, Reason: plan.Reason, DefaultState: plan.DefaultState, CurrentSha256: plan.CurrentSHA256, CandidateSha256: plan.CandidateSHA256, DefaultMayChange: plan.DefaultMayChange, ExistingConnectionsRetained: plan.ExistingConnectionsRetained, NativeResourcesMayRemain: plan.NativeResourcesMayRemain, NextAction: plan.NextAction, Validation: plan.Validation, TrafficGapPossible: plan.TrafficGapPossible, CreatedAt: timestamppb.New(plan.CreatedAt), ExpiresAt: timestamppb.New(plan.ExpiresAt)}, nil
}
func (h Handler) ApplyOutboundReplacement(ctx context.Context, request *v1.OutboundReplacementRequest) (*v1.OutboundReplacementResult, error) {
	if request == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Replacements.Apply(ctx, request.InstanceId, request.PlanId)
	if result.PlanID == "" && err != nil {
		return nil, failure(err)
	}
	return replacementResult(result), nil
}
func (h Handler) GetOutboundReplacement(ctx context.Context, request *v1.OutboundReplacementRequest) (*v1.OutboundReplacementResult, error) {
	if request == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	result, err := h.Replacements.Result(ctx, request.InstanceId, request.PlanId)
	if err != nil {
		return nil, failure(err)
	}
	return replacementResult(result), nil
}
func replacementResult(result domain.ReplacementResult) *v1.OutboundReplacementResult {
	response := &v1.OutboundReplacementResult{PlanId: result.PlanID, InstanceId: result.InstanceID, Generation: result.Generation, Tag: result.Tag, Outcome: result.Outcome, Error: result.Error, RequiresInstanceRestart: result.RequiresInstanceRestart, DefaultMayChange: result.DefaultMayChange, NativeResourcesMayRemain: result.NativeResourcesMayRemain}
	if !result.StartedAt.IsZero() {
		response.StartedAt = timestamppb.New(result.StartedAt)
	}
	if result.CompletedAt != nil {
		response.CompletedAt = timestamppb.New(*result.CompletedAt)
	}
	for _, step := range result.Steps {
		wire := &v1.OutboundReplacementStep{Kind: step.Kind, Effect: step.Effect, Attempted: step.Attempted, Acknowledged: step.Acknowledged, RequiresInstanceRestart: step.RequiresInstanceRestart, Error: step.Error}
		if step.CompletedAt != nil {
			wire.CompletedAt = timestamppb.New(*step.CompletedAt)
		}
		response.Steps = append(response.Steps, wire)
	}
	return response
}
