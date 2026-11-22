package grpcapi

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/proto/v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (h Handler) AddAccount(ctx context.Context, request *v1.AddAccountRequest) (*v1.AccountOperation, error) {
	if request == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Add(ctx, request.InstanceId, request.InboundTag, request.Configuration)
	return mutationResult(result, err)
}
func (h Handler) RemoveAccount(ctx context.Context, request *v1.RemoveAccountRequest) (*v1.AccountOperation, error) {
	if request == nil || request.Email == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Remove(ctx, request.InstanceId, request.InboundTag, *request.Email)
	return mutationResult(result, err)
}
func (h Handler) GetAccountControlStatus(ctx context.Context, request *v1.GetAccountControlStatusRequest) (*v1.AccountControlStatus, error) {
	if request == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Status(ctx, request.InstanceId)
	if err != nil {
		return nil, failure(err)
	}
	response := &v1.AccountControlStatus{InstanceId: result.InstanceID, Generation: result.Generation, ExecutionState: result.ExecutionState, ApiConfigured: result.APIConfigured, RequestInFlight: result.RequestInFlight, RequiresInstanceRestart: result.RequiresInstanceRestart, ObservedAt: timestamppb.New(result.ObservedAt)}
	if result.LastOperation != nil {
		response.LastOperation = operationResult(*result.LastOperation)
	}
	return response, nil
}
func operationResult(result domain.Operation) *v1.AccountOperation {
	response := &v1.AccountOperation{Guidance: operationGuidance(result.Guidance), InstanceId: result.InstanceID, InboundTag: result.InboundTag, Generation: result.Generation, Kind: result.Kind, Email: result.Email, Effect: string(result.Effect), Acknowledged: result.Acknowledged, RequiresInstanceRestart: result.RequiresInstanceRestart, DurationNs: int64(result.Duration)}
	if !result.ObservedAt.IsZero() {
		response.ObservedAt = timestamppb.New(result.ObservedAt)
	}
	return response
}
func mutationResult(result domain.Operation, err error) (*v1.AccountOperation, error) {
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
