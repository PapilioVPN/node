package grpcapi

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/app/validators"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcidentity"
	"github.com/PapilioVPN/node/internal/shared/grpcresponse"
)

type BalancerOverrider interface {
	OverrideBalancerTarget(context.Context, instances.InstanceID, domain.BalancerOverride) (domain.InstanceBalancerOverride, error)
}

func (h Handler) OverrideBalancerTarget(ctx context.Context, request *xrayv1.OverrideBalancerTargetRequest) (*xrayv1.OverrideBalancerTargetResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, routingMutationFailure(err, false)
	}
	if !h.AllowRoutingManagement {
		return nil, routingMutationFailure(status.Error(codes.PermissionDenied, "engine routing management is not granted"), false)
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" || request.Target == nil {
		return nil, routingMutationFailure(status.Error(codes.InvalidArgument, "instance_id and explicit target are required"), false)
	}
	selection := domain.BalancerOverride{BalancerTag: request.BalancerTag, Target: *request.Target}
	if err := validators.BalancerOverride(selection); err != nil {
		return nil, routingMutationFailure(status.Error(codes.InvalidArgument, "valid balancer_tag and target are required"), false)
	}
	result, err := h.BalancerOverrides.OverrideBalancerTarget(ctx, instances.InstanceID(request.InstanceId), selection)
	if err != nil {
		return nil, routingMutationError(ctx, err)
	}
	// Keep confirmed completion after late cancellation; external delivery can fail.
	return &xrayv1.OverrideBalancerTargetResponse{
		InstanceId: string(result.InstanceID), BalancerTag: result.Override.BalancerTag,
		Target: result.Override.Target, AcknowledgedAtUnixMs: result.Acknowledgement.AcknowledgedAt.UnixMilli(),
	}, nil
}

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
	case errors.Is(cause, domain.ErrInvalidBalancerOverride):
		code, message, unknown = codes.InvalidArgument, "invalid Xray balancer override", false
	case errors.Is(cause, domain.ErrAPIBusy):
		code, message, unknown = codes.ResourceExhausted, "Xray API is busy", false
	case errors.Is(cause, domain.ErrAPIResourceLimit):
		code, message = codes.ResourceExhausted, "Xray API resource limit exceeded"
	case errors.Is(cause, grpc.ErrClientConnClosing):
		unknown = false
	case errors.Is(cause, context.Canceled), errors.Is(cause, context.DeadlineExceeded):
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
