package grpcapi

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/core/users/app/validators"
	"github.com/PapilioVPN/node/internal/modules/core/users/domain"
	userv1 "github.com/PapilioVPN/node/internal/modules/core/users/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcidentity"
)

type InboundUserRemover interface {
	RemoveInboundUser(context.Context, instances.InstanceID, domain.InboundUserSelection) (domain.InstanceInboundUserMutation, error)
}

func (h UserAPI) RemoveInboundUser(ctx context.Context, request *userv1.RemoveInboundUserRequest) (*userv1.RemoveInboundUserResponse, error) {
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
		return nil, userMutationError(ctx, err)
	}
	return &userv1.RemoveInboundUserResponse{InstanceId: string(result.InstanceID), InboundTag: result.Selection.InboundTag, Email: result.Selection.Email, AcknowledgedAtUnixMs: result.Acknowledgement.AcknowledgedAt.UnixMilli()}, nil
}
