package mutations

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	userv1 "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcidentity"
)

type InboundUsersAdder interface {
	AddInboundUsers(context.Context, instances.InstanceID, domain.InboundUserAdditions) (domain.InstanceInboundUserAdditions, error)
}

func (h MutationAPI) AddInboundUsers(ctx context.Context, request *userv1.AddInboundUsersRequest) (*userv1.AddInboundUsersResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, err
	}
	if !h.AllowUserManagement {
		return nil, status.Error(codes.PermissionDenied, "engine user management is not granted")
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" || validators.InboundTag(request.GetInboundTag()) != nil {
		return nil, status.Error(codes.InvalidArgument, "instance_id and inbound_tag are required")
	}
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	input := domain.InboundUserAdditions{InboundTag: request.InboundTag, Users: make([]domain.InboundUser, len(request.Users))}
	for i, user := range request.Users {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
		input.Users[i] = inboundUser(user)
		if err := validators.InboundUser(input.Users[i]); err != nil {
			return nil, status.Error(codes.InvalidArgument, "one valid email and typed account are required for every user")
		}
	}
	result, err := h.UserAdditions.AddInboundUsers(ctx, instances.InstanceID(request.InstanceId), input)
	response := encodeUserAdditions(result)
	if err == nil {
		return response, nil
	}
	outcome := userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_NOT_APPLIED
	if errors.Is(err, domain.ErrUserMutationUnknown) {
		outcome = userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_UNKNOWN
	}
	for _, item := range result.Report.Items {
		if item.State == domain.UserMutationAcknowledged || item.State == domain.UserMutationUnknown {
			outcome = userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_UNKNOWN
			break
		}
	}
	mapped := userMutationStatus(ctx, err)
	detailed, detailError := mapped.WithDetails(&userv1.UserAdditionsFailure{Progress: response, Outcome: outcome})
	if detailError != nil {
		return nil, mapped.Err()
	}
	return nil, detailed.Err()
}

func encodeUserAdditions(result domain.InstanceInboundUserAdditions) *userv1.AddInboundUsersResponse {
	response := &userv1.AddInboundUsersResponse{InstanceId: string(result.InstanceID), InboundTag: result.InboundTag, Items: make([]*userv1.UserAdditionResult, len(result.Report.Items))}
	for i, item := range result.Report.Items {
		entry := &userv1.UserAdditionResult{Email: item.Email}
		switch item.State {
		case domain.UserMutationNotAttempted:
			entry.State = userv1.UserAdditionState_USER_ADDITION_STATE_NOT_ATTEMPTED
		case domain.UserMutationAcknowledged:
			entry.State = userv1.UserAdditionState_USER_ADDITION_STATE_ACKNOWLEDGED
		case domain.UserMutationNotApplied:
			entry.State = userv1.UserAdditionState_USER_ADDITION_STATE_NOT_APPLIED
		case domain.UserMutationUnknown:
			entry.State = userv1.UserAdditionState_USER_ADDITION_STATE_UNKNOWN
		}
		if !item.AcknowledgedAt.IsZero() {
			value := item.AcknowledgedAt.UnixMilli()
			entry.AcknowledgedAtUnixMs = &value
		}
		response.Items[i] = entry
	}
	return response
}
