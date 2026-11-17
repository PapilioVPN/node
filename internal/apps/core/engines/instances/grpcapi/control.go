package grpcapi

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	instancesv1 "github.com/PapilioVPN/node/internal/apps/core/engines/instances/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcidentity"
	"github.com/PapilioVPN/node/internal/shared/grpcresponse"
)

// InstanceController is the application lifecycle contract consumed by this adapter.
type InstanceController interface {
	StartInstance(context.Context, domain.InstanceID) (domain.Status, error)
	StopInstance(context.Context, domain.InstanceID) (domain.Status, error)
}

func (h Handler) StartInstance(ctx context.Context, request *instancesv1.StartInstanceRequest) (*instancesv1.InstanceStatus, error) {
	if err := h.authorizeControl(ctx, request.GetInstanceId()); err != nil {
		return nil, err
	}
	value, err := h.Control.StartInstance(ctx, domain.InstanceID(request.GetInstanceId()))
	return controlResult(value, err)
}

func (h Handler) StopInstance(ctx context.Context, request *instancesv1.StopInstanceRequest) (*instancesv1.InstanceStatus, error) {
	if err := h.authorizeControl(ctx, request.GetInstanceId()); err != nil {
		return nil, err
	}
	value, err := h.Control.StopInstance(ctx, domain.InstanceID(request.GetInstanceId()))
	return controlResult(value, err)
}

func (h Handler) authorizeControl(ctx context.Context, id string) error {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return err
	}
	if !h.AllowControl {
		return status.Error(codes.PermissionDenied, "instance control is not granted")
	}
	if strings.TrimSpace(id) == "" {
		return status.Error(codes.InvalidArgument, "instance_id is required")
	}
	return nil
}

func controlResult(value domain.Status, cause error) (*instancesv1.InstanceStatus, error) {
	if cause == nil {
		return instanceStatus(value)
	}
	failure := status.New(codes.Internal, "cannot control instance")
	if mapped := grpcresponse.ContextError(cause); mapped != nil {
		failure = status.Convert(mapped)
	} else if errors.Is(cause, domain.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "instance not found")
	}
	if value.Instance.ID == "" {
		return nil, failure.Err()
	}
	// Native gRPC discards response messages on errors. Preserve a selected
	// runtime's partial observation as a typed, sanitized status detail instead.
	outcome, err := instanceStatus(value)
	if err != nil {
		return nil, failure.Err()
	}
	detailed, err := failure.WithDetails(outcome)
	if err != nil {
		return nil, failure.Err()
	}
	return nil, detailed.Err()
}
