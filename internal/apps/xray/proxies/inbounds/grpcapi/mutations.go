package grpcapi

import (
	"context"
	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	inboundsv1 "github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/grpcapi/proto/v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (h Handler) AddInbound(ctx context.Context, request *inboundsv1.AddInboundRequest) (*inboundsv1.InboundOperation, error) {
	if request == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Add(ctx, request.InstanceId, request.Configuration)
	return mutationResult(result, err)
}
func (h Handler) RemoveInbound(ctx context.Context, request *inboundsv1.RemoveInboundRequest) (*inboundsv1.InboundOperation, error) {
	if request == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Remove(ctx, request.InstanceId, request.Tag)
	return mutationResult(result, err)
}
func operationResult(result domain.Operation) *inboundsv1.InboundOperation {
	response := &inboundsv1.InboundOperation{Replacement: replacementProgress(result.Replacement), InstanceId: result.InstanceID, Generation: result.Generation, Kind: result.Kind, Tag: result.Tag, Effect: string(result.Effect), Acknowledged: result.Acknowledged, RequiresInstanceRestart: result.RequiresInstanceRestart, DurationNs: int64(result.Duration)}
	if !result.ObservedAt.IsZero() {
		response.ObservedAt = timestamppb.New(result.ObservedAt)
	}
	return response
}
func mutationResult(result domain.Operation, err error) (*inboundsv1.InboundOperation, error) {
	response := operationResult(result)
	if err == nil {
		return response, nil
	}
	detailed, detailErr := status.Convert(failure(err)).WithDetails(response)
	if detailErr != nil {
		return nil, failure(err)
	}
	return nil, detailed.Err()
}

func (h Handler) AddInboundFromSources(ctx context.Context, request *inboundsv1.AddInboundFromSourcesRequest) (*inboundsv1.InboundOperation, error) {
	if request == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	if len(request.Sources) > configs.MaxSources {
		return nil, failure(domain.ErrCapacity)
	}
	sources := make([]configs.Source, len(request.Sources))
	for i, source := range request.Sources {
		if source == nil {
			return nil, failure(domain.ErrInvalidArgument)
		}
		sources[i] = configs.Source{Name: source.Name, Content: source.Content}
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Sources.Add(ctx, request.InstanceId, request.Tag, sources)
	return mutationResult(result, err)
}

func replacementProgress(p *domain.ReplacementProgress) *inboundsv1.InboundReplacementProgress {
	if p == nil {
		return nil
	}
	return &inboundsv1.InboundReplacementProgress{Token: p.Token, NextAction: p.NextAction, Remove: &inboundsv1.InboundReplacementStep{Attempted: p.Remove.Attempted, Effect: string(p.Remove.Effect), Acknowledged: p.Remove.Acknowledged}, Add: &inboundsv1.InboundReplacementStep{Attempted: p.Add.Attempted, Effect: string(p.Add.Effect), Acknowledged: p.Add.Acknowledged}}
}
