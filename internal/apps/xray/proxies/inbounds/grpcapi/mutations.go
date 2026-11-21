package grpcapi

import (
	"context"

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
	response := &inboundsv1.InboundOperation{InstanceId: result.InstanceID, Generation: result.Generation, Kind: result.Kind, Tag: result.Tag, Effect: string(result.Effect), Acknowledged: result.Acknowledged, RequiresInstanceRestart: result.RequiresInstanceRestart, DurationNs: int64(result.Duration)}
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
