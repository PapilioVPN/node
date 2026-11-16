package grpcapi

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcresponse"
)

func routingMutationError(ctx context.Context, cause error) error {
	mapped := grpcresponse.ContextError(errors.Join(cause, ctx.Err()))
	code, message, unknown := codes.Unavailable, "cannot change Xray routing", true
	switch {
	case errors.Is(cause, instances.ErrNotFound):
		code, message, unknown = codes.NotFound, "Xray instance not found", false
	case errors.Is(cause, domain.ErrAPIDisabled):
		code, message, unknown = codes.FailedPrecondition, "Xray API is not enabled", false
	case errors.Is(cause, domain.ErrRoutingManagementUnsupported):
		code, message, unknown = codes.FailedPrecondition, "Xray routing management is not supported", false
	case errors.Is(cause, domain.ErrRoutingRulePreparationDisabled):
		code, message, unknown = codes.FailedPrecondition, "Xray routing rule preparation is not enabled", false
	case errors.Is(cause, domain.ErrInvalidRoutingRuleMode), errors.Is(cause, domain.ErrInvalidRoutingRule):
		code, message, unknown = codes.InvalidArgument, "invalid native routing rule or mode", false
	case errors.Is(cause, domain.ErrInvalidPreparedRoutingRule):
		code, message, unknown = codes.FailedPrecondition, "unrecognized native routing build output", false
	case errors.Is(cause, domain.ErrRoutingRulePreparationBusy):
		code, message, unknown = codes.ResourceExhausted, "Xray routing rule preparation is busy", false
	case errors.Is(cause, domain.ErrRoutingRuleInputLimit), errors.Is(cause, domain.ErrRoutingRuleOutputLimit):
		code, message, unknown = codes.ResourceExhausted, "Xray routing rule preparation resource limit exceeded", false
	case errors.Is(cause, domain.ErrInvalidBalancerOverride):
		code, message, unknown = codes.InvalidArgument, "invalid Xray balancer override", false
	case errors.Is(cause, domain.ErrInvalidRoutingRuleTag):
		code, message, unknown = codes.InvalidArgument, "invalid Xray routing rule tag", false
	case errors.Is(cause, domain.ErrAPIBusy):
		code, message, unknown = codes.ResourceExhausted, "Xray API is busy", false
	case errors.Is(cause, domain.ErrAPIResourceLimit):
		code, message = codes.ResourceExhausted, "Xray API resource limit exceeded"
	case errors.Is(cause, grpc.ErrClientConnClosing):
		unknown = false
	case errors.Is(cause, context.Canceled), errors.Is(cause, context.DeadlineExceeded):
		unknown = false
	}
	if errors.Is(cause, domain.ErrRoutingRulePreparationFailed) {
		unknown = false
	}
	if errors.Is(cause, domain.ErrRoutingMutationUnknown) {
		unknown = true
	}
	if mapped == nil {
		mapped = status.Error(code, message)
	}
	return routingMutationFailure(mapped, unknown)
}

func routingMutationFailure(mapped error, unknown bool) error {
	outcome := xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED
	if unknown {
		outcome = xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_UNKNOWN
	}
	detailed, err := status.Convert(mapped).WithDetails(&xrayv1.RoutingMutationFailure{Outcome: outcome})
	if err != nil {
		return mapped
	}
	return detailed.Err()
}
