package grpcapi

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/routing/domain"
	routingv1 "github.com/PapilioVPN/node/internal/modules/xray/routing/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcidentity"
	"github.com/PapilioVPN/node/internal/shared/grpcresponse"
)

type RoutingProber interface {
	TestRoute(context.Context, instances.InstanceID, domain.RoutingProbe) (domain.InstanceRoutingProbe, error)
}

func (h RoutingAPI) TestRoute(ctx context.Context, request *routingv1.TestRouteRequest) (*routingv1.TestRouteResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, routingProbeFailure(err, false)
	}
	if !h.AllowRoutingTest {
		return nil, routingProbeFailure(status.Error(codes.PermissionDenied, "engine routing test is not granted"), false)
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" {
		return nil, routingProbeFailure(status.Error(codes.InvalidArgument, "instance_id is required"), false)
	}
	input, err := decodeRoutingContext(request.Context)
	if err != nil {
		return nil, routingProbeFailure(status.Error(codes.InvalidArgument, "valid routing context, network, unzoned IPs and ports are required"), false)
	}
	result, err := h.Probes.TestRoute(ctx, instances.InstanceID(request.InstanceId), domain.RoutingProbe{
		Context: input, FieldSelectors: request.FieldSelectors, PublishResult: request.PublishResult,
	})
	if err != nil {
		return nil, routingProbeError(ctx, err)
	}
	// A confirmed native result survives late cancellation; delivery can still fail.
	return &routingv1.TestRouteResponse{
		InstanceId: string(result.InstanceID), AcknowledgedAtUnixMs: result.Result.AcknowledgedAt.UnixMilli(),
		Route: &routingv1.RoutingResult{
			Context:     encodeRoutingContext(result.Result.Route.Context),
			OutboundTag: result.Result.Route.OutboundTag, OutboundGroupTags: result.Result.Route.OutboundGroupTags,
		},
	}, nil
}

func routingProbeError(ctx context.Context, cause error) error {
	mapped := grpcresponse.ContextError(errors.Join(cause, ctx.Err()))
	code, message, unknown := codes.Unavailable, "cannot test Xray routing", true
	switch {
	case errors.Is(cause, instances.ErrNotFound):
		code, message, unknown = codes.NotFound, "Xray instance not found", false
	case errors.Is(cause, domain.ErrAPIDisabled):
		code, message, unknown = codes.FailedPrecondition, "Xray API is not enabled", false
	case errors.Is(cause, domain.ErrRoutingProbeUnsupported):
		code, message, unknown = codes.FailedPrecondition, "Xray routing probes are not supported", false
	case errors.Is(cause, domain.ErrInvalidRoutingProbe):
		code, message, unknown = codes.InvalidArgument, "invalid Xray routing probe", false
	case errors.Is(cause, domain.ErrAPIBusy):
		code, message, unknown = codes.ResourceExhausted, "Xray API is busy", false
	case errors.Is(cause, domain.ErrAPIResourceLimit):
		code, message = codes.ResourceExhausted, "Xray API resource limit exceeded"
	case errors.Is(cause, grpc.ErrClientConnClosing):
		unknown = false
	case errors.Is(cause, context.Canceled), errors.Is(cause, context.DeadlineExceeded):
		unknown = false
	}
	if errors.Is(cause, domain.ErrRoutingProbeUnknown) {
		unknown = true
	}
	if mapped == nil {
		mapped = status.Error(code, message)
	}
	return routingProbeFailure(mapped, unknown)
}

func routingProbeFailure(mapped error, unknown bool) error {
	outcome := routingv1.RoutingProbeOutcome_ROUTING_PROBE_OUTCOME_NOT_EXECUTED
	if unknown {
		outcome = routingv1.RoutingProbeOutcome_ROUTING_PROBE_OUTCOME_UNKNOWN
	}
	detailed, err := status.Convert(mapped).WithDetails(&routingv1.TestRouteFailure{Outcome: outcome})
	if err != nil {
		return mapped
	}
	return detailed.Err()
}
