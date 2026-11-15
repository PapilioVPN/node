package grpcapi

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcresponse"
)

func userMutationError(ctx context.Context, cause error) error {
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
		case errors.Is(cause, domain.ErrUserManagementUnsupported):
			code, message = codes.FailedPrecondition, "Xray user management is not supported"
		case errors.Is(cause, domain.ErrInboundProtocolMismatch):
			code, message = codes.FailedPrecondition, "account does not match Xray inbound protocol"
		case errors.Is(cause, domain.ErrAPIBusy), errors.Is(cause, domain.ErrAPIResourceLimit):
			code, message = codes.ResourceExhausted, "Xray API resource limit exceeded"
		case errors.Is(cause, domain.ErrInvalidUserSelection), errors.Is(cause, domain.ErrInvalidUserAccount):
			code, message = codes.InvalidArgument, "invalid Xray user selection or account"
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
