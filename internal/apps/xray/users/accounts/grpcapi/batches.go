package grpcapi

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/proto/v1"
	"google.golang.org/grpc/status"
)

func (h Handler) AddAccounts(ctx context.Context, request *v1.AddAccountsRequest) (*v1.AccountBatch, error) {
	if request == nil || request.Parallelism > domain.MaxBatchParallelism {
		return nil, failure(domain.ErrInvalidArgument)
	}
	if len(request.Items) > domain.MaxBatchItems {
		return nil, failure(domain.ErrCapacity)
	}
	items := make([]domain.Addition, len(request.Items))
	for i, item := range request.Items {
		if item == nil {
			return nil, failure(domain.ErrInvalidArgument)
		}
		items[i] = domain.Addition{InboundTag: item.InboundTag, Configuration: item.Configuration}
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.AddMany(ctx, request.InstanceId, items, int(request.Parallelism))
	return batchResult(result, err)
}
func (h Handler) RemoveAccounts(ctx context.Context, request *v1.RemoveAccountsRequest) (*v1.AccountBatch, error) {
	if request == nil || request.Parallelism > domain.MaxBatchParallelism {
		return nil, failure(domain.ErrInvalidArgument)
	}
	if len(request.Items) > domain.MaxBatchItems {
		return nil, failure(domain.ErrCapacity)
	}
	items := make([]domain.Removal, len(request.Items))
	for i, item := range request.Items {
		if item == nil || item.Email == nil {
			return nil, failure(domain.ErrInvalidArgument)
		}
		items[i] = domain.Removal{InboundTag: item.InboundTag, Email: *item.Email}
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.RemoveMany(ctx, request.InstanceId, items, int(request.Parallelism))
	return batchResult(result, err)
}
func batchResult(result domain.Batch, cause error) (*v1.AccountBatch, error) {
	if len(result.Items) == 0 && cause != nil {
		return nil, failure(cause)
	}
	response := &v1.AccountBatch{Operation: operationResult(result.Operation), Items: make([]*v1.AccountBatchItem, len(result.Items))}
	if cause != nil {
		response.Error = status.Convert(failure(cause)).Message()
	}
	for i, item := range result.Items {
		code := ""
		if errors.Is(item.Err, domain.ErrNotAttempted) {
			code = "not_attempted"
		} else if item.Err != nil {
			code = status.Convert(failure(item.Err)).Message()
		}
		response.Items[i] = &v1.AccountBatchItem{Index: uint32(item.Index), Attempted: item.Attempted, Operation: operationResult(item.Operation), Error: code}
	}
	return response, nil
}
