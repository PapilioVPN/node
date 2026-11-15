// Package grpcapi adapts instance lifecycle operations to the versioned gRPC contract.
package grpcapi

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/instances/domain"
	instancesv1 "github.com/PapilioVPN/node/internal/modules/instances/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcresponse"
)

type InstanceStatusReader interface {
	GetInstanceStatus(context.Context, domain.InstanceID) (domain.Status, error)
}

type Handler struct {
	instancesv1.UnimplementedInstanceServiceServer
	Status          InstanceStatusReader
	Statuses        InstanceStatusesReader
	Control         InstanceController
	AllowStatusRead bool
	AllowControl    bool
}

func (h Handler) GetInstanceStatus(ctx context.Context, request *instancesv1.GetInstanceStatusRequest) (*instancesv1.GetInstanceStatusResponse, error) {
	if err := verifyClient(ctx); err != nil {
		return nil, err
	}
	if !h.AllowStatusRead {
		return nil, status.Error(codes.PermissionDenied, "instance status read is not granted")
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "instance_id is required")
	}
	value, err := h.Status.GetInstanceStatus(ctx, domain.InstanceID(request.GetInstanceId()))
	if mapped := grpcresponse.ContextError(err); mapped != nil {
		return nil, mapped
	}
	if errors.Is(err, domain.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "instance not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot read instance status")
	}
	return statusResponse(value)
}
