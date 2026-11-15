package grpcapi

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/app/validators"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcidentity"
	"github.com/PapilioVPN/node/internal/shared/grpcresponse"
)

type InboundUserRemover interface {
	RemoveInboundUser(context.Context, instances.InstanceID, domain.InboundUserSelection) (domain.InstanceInboundUserMutation, error)
}

func (h Handler) RemoveInboundUser(ctx context.Context, request *xrayv1.RemoveInboundUserRequest) (*xrayv1.RemoveInboundUserResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, err
	}
	if !h.AllowUserManagement {
		return nil, status.Error(codes.PermissionDenied, "engine user management is not granted")
	}
	selection := domain.InboundUserSelection{InboundTag: request.GetInboundTag(), Email: request.GetEmail()}
	if strings.TrimSpace(request.GetInstanceId()) == "" || validators.InboundUserSelection(selection) != nil {
		return nil, status.Error(codes.InvalidArgument, "instance_id, inbound_tag and email are required")
	}
	result, err := h.UserRemoval.RemoveInboundUser(ctx, instances.InstanceID(request.InstanceId), selection)
	if err != nil {
		return nil, userRemovalError(ctx, err)
	}
	return &xrayv1.RemoveInboundUserResponse{InstanceId: string(result.InstanceID), InboundTag: result.Selection.InboundTag, Email: result.Selection.Email, AcknowledgedAtUnixMs: result.Acknowledgement.AcknowledgedAt.UnixMilli()}, nil
}

func userRemovalError(ctx context.Context, cause error) error {
	mapped := grpcresponse.ContextError(errors.Join(cause, ctx.Err()))
	if mapped == nil {
		code, message := codes.Unavailable, "cannot remove Xray inbound user"
		switch {
		case errors.Is(cause, instances.ErrNotFound):
			code, message = codes.NotFound, "Xray instance not found"
		case errors.Is(cause, domain.ErrAPIDisabled):
			code, message = codes.FailedPrecondition, "Xray API is not enabled"
		case errors.Is(cause, domain.ErrUserManagementUnsupported):
			code, message = codes.FailedPrecondition, "Xray user management is not supported"
		case errors.Is(cause, domain.ErrAPIBusy), errors.Is(cause, domain.ErrAPIResourceLimit):
			code, message = codes.ResourceExhausted, "Xray API resource limit exceeded"
		case errors.Is(cause, domain.ErrInvalidUserSelection):
			code, message = codes.InvalidArgument, "invalid Xray user selection"
		}
		mapped = status.Error(code, message)
	}
	outcome := xrayv1.UserMutationOutcome_USER_MUTATION_OUTCOME_NOT_APPLIED
	if errors.Is(cause, domain.ErrUserMutationUnknown) {
		outcome = xrayv1.UserMutationOutcome_USER_MUTATION_OUTCOME_UNKNOWN
	}
	detailed, err := status.Convert(mapped).WithDetails(&xrayv1.UserMutationFailure{Outcome: outcome})
	if err != nil {
		return mapped
	}
	return detailed.Err()
}
