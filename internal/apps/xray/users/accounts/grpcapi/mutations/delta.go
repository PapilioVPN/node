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

type InboundUserDeltaApplier interface {
	ApplyInboundUserDelta(context.Context, instances.InstanceID, domain.InboundUserDelta) (domain.InstanceInboundUserDelta, error)
}

func (h MutationAPI) ApplyInboundUserDelta(ctx context.Context, request *userv1.ApplyInboundUserDeltaRequest) (*userv1.ApplyInboundUserDeltaResponse, error) {
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
	input := domain.InboundUserDelta{InboundTag: request.InboundTag, RemoveEmails: request.RemoveEmails, Users: make([]domain.InboundUser, len(request.Users))}
	for i, user := range request.Users {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
		input.Users[i] = inboundUser(user)
		if err := validators.InboundUser(input.Users[i]); err != nil {
			return nil, status.Error(codes.InvalidArgument, "one valid email and typed account are required for every user")
		}
	}
	for _, email := range input.RemoveEmails {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
		if err := validators.InboundUserEmail(email); err != nil {
			return nil, status.Error(codes.InvalidArgument, "a valid email is required for every removal")
		}
	}
	result, err := h.UserDelta.ApplyInboundUserDelta(ctx, instances.InstanceID(request.InstanceId), input)
	response := encodeUserDelta(result)
	if err == nil {
		return response, nil
	}
	outcome := userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_NOT_APPLIED
	if errors.Is(err, domain.ErrUserMutationUnknown) {
		outcome = userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_UNKNOWN
	}
	for _, step := range result.Report.Steps {
		if step.State == domain.UserMutationAcknowledged || step.State == domain.UserMutationUnknown {
			outcome = userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_UNKNOWN
			break
		}
	}
	mapped := userMutationStatus(ctx, err)
	detailed, detailError := mapped.WithDetails(&userv1.UserDeltaFailure{Progress: response, Outcome: outcome})
	if detailError != nil {
		return nil, mapped.Err()
	}
	return nil, detailed.Err()
}

func encodeUserDelta(result domain.InstanceInboundUserDelta) *userv1.ApplyInboundUserDeltaResponse {
	response := &userv1.ApplyInboundUserDeltaResponse{InstanceId: string(result.InstanceID), InboundTag: result.InboundTag, Steps: make([]*userv1.UserDeltaStep, len(result.Report.Steps))}
	if !result.Report.ObservedAt.IsZero() {
		value := result.Report.ObservedAt.UnixMilli()
		response.ObservedAtUnixMs = &value
	}
	for i, step := range result.Report.Steps {
		entry := &userv1.UserDeltaStep{Email: step.Email}
		switch step.Action {
		case domain.UserMutationRemove:
			entry.Action = userv1.UserDeltaAction_USER_DELTA_ACTION_REMOVE
		case domain.UserMutationAdd:
			entry.Action = userv1.UserDeltaAction_USER_DELTA_ACTION_ADD
		}
		switch step.State {
		case domain.UserMutationNotAttempted:
			entry.State = userv1.UserDeltaState_USER_DELTA_STATE_NOT_ATTEMPTED
		case domain.UserMutationAcknowledged:
			entry.State = userv1.UserDeltaState_USER_DELTA_STATE_ACKNOWLEDGED
		case domain.UserMutationNotApplied:
			entry.State = userv1.UserDeltaState_USER_DELTA_STATE_NOT_APPLIED
		case domain.UserMutationUnknown:
			entry.State = userv1.UserDeltaState_USER_DELTA_STATE_UNKNOWN
		case domain.UserMutationNotRequired:
			entry.State = userv1.UserDeltaState_USER_DELTA_STATE_NOT_REQUIRED
		}
		if !step.AcknowledgedAt.IsZero() {
			value := step.AcknowledgedAt.UnixMilli()
			entry.AcknowledgedAtUnixMs = &value
		}
		response.Steps[i] = entry
	}
	return response
}
