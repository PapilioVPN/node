package mutations

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	userv1 "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcresponse"
)

func userMutationError(ctx context.Context, cause error) error {
	mapped := userMutationStatus(ctx, cause).Err()
	outcome := userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_NOT_APPLIED
	if errors.Is(cause, domain.ErrUserMutationUnknown) {
		outcome = userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_UNKNOWN
	}
	detailed, err := status.Convert(mapped).WithDetails(&userv1.UserMutationFailure{Outcome: outcome})
	if err != nil {
		return mapped
	}
	return detailed.Err()
}

func userMutationStatus(ctx context.Context, cause error) *status.Status {
	mapped := grpcresponse.ContextError(errors.Join(cause, ctx.Err()))
	if mapped == nil {
		code, message := codes.Unavailable, "cannot change Xray inbound user"
		switch {
		case errors.Is(cause, instances.ErrNotFound):
			code, message = codes.NotFound, "Xray instance not found"
		case errors.Is(cause, domain.ErrInboundNotFound):
			code, message = codes.NotFound, "Xray inbound not found"
		case errors.Is(cause, domain.ErrAPIDisabled):
			code, message = codes.FailedPrecondition, "Xray API is not enabled"
		case errors.Is(cause, domain.ErrUserCollectionsDisabled):
			code, message = codes.FailedPrecondition, "Xray user collections are not configured"
		case errors.Is(cause, domain.ErrUserCollectionTooLarge):
			code, message = codes.ResourceExhausted, "Xray user collection limit exceeded"
		case errors.Is(cause, domain.ErrUserManagementUnsupported), errors.Is(cause, domain.ErrUserReadUnsupported):
			code, message = codes.FailedPrecondition, "Xray user management is not supported"
		case errors.Is(cause, domain.ErrInboundProtocolMismatch):
			code, message = codes.FailedPrecondition, "account does not match Xray inbound protocol"
		case errors.Is(cause, domain.ErrAPIBusy), errors.Is(cause, domain.ErrAPIResourceLimit):
			code, message = codes.ResourceExhausted, "Xray API resource limit exceeded"
		case errors.Is(cause, domain.ErrInvalidUserSelection), errors.Is(cause, domain.ErrInvalidUserAccount), errors.Is(cause, domain.ErrInvalidUserDelta):
			code, message = codes.InvalidArgument, "invalid Xray user selection or account"
		}
		mapped = status.Error(code, message)
	}
	return status.Convert(mapped)
}
