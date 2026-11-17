package grpcapi

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	instancesv1 "github.com/PapilioVPN/node/internal/apps/core/engines/instances/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcidentity"
	"github.com/PapilioVPN/node/internal/shared/grpcresponse"
)

// InstanceStatusesReader is the bounded collection operation consumed here.
type InstanceStatusesReader interface {
	ListInstanceStatuses(context.Context, domain.StatusPageInput) (domain.StatusPage, error)
}

func (h Handler) ListInstanceStatuses(ctx context.Context, request *instancesv1.ListInstanceStatusesRequest) (*instancesv1.ListInstanceStatusesResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, err
	}
	if !h.AllowStatusRead {
		return nil, status.Error(codes.PermissionDenied, "instance status read is not granted")
	}
	page, err := h.Statuses.ListInstanceStatuses(ctx, domain.StatusPageInput{
		AfterID: domain.InstanceID(request.GetAfterInstanceId()), PageSize: int(request.GetPageSize()),
	})
	if mapped := grpcresponse.ContextError(err); mapped != nil {
		return nil, mapped
	}
	if errors.Is(err, domain.ErrInvalidInput) {
		return nil, status.Error(codes.InvalidArgument, "page_size must be positive")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot list instance statuses")
	}
	response := &instancesv1.ListInstanceStatusesResponse{
		Items: make([]*instancesv1.InstanceStatus, 0, len(page.Items)), NextAfterInstanceId: string(page.NextAfterID),
	}
	for _, value := range page.Items {
		if mapped := grpcresponse.ContextError(ctx.Err()); mapped != nil {
			return nil, mapped
		}
		item, err := instanceStatus(value)
		if err != nil {
			return nil, err
		}
		response.Items = append(response.Items, item)
	}
	if mapped := grpcresponse.ContextError(ctx.Err()); mapped != nil {
		return nil, mapped
	}
	return response, nil
}
