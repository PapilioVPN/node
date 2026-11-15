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

type TrafficCounterReader interface {
	GetTrafficCounter(context.Context, instances.InstanceID, domain.TrafficSelection) (domain.InstanceTrafficCounter, error)
}

func (h Handler) GetTrafficCounter(ctx context.Context, request *xrayv1.GetTrafficCounterRequest) (*xrayv1.GetTrafficCounterResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, err
	}
	if !h.AllowStatsRead {
		return nil, status.Error(codes.PermissionDenied, "engine statistics read is not granted")
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "instance_id is required")
	}
	selection := domain.TrafficSelection{Name: request.GetName(), Scope: decodeTrafficScope(request.GetScope())}
	switch request.GetDirection() {
	case xrayv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK:
		selection.Direction = domain.Uplink
	case xrayv1.TrafficDirection_TRAFFIC_DIRECTION_DOWNLINK:
		selection.Direction = domain.Downlink
	}
	if err := validators.TrafficSelection(selection); err != nil {
		return nil, status.Error(codes.InvalidArgument, "valid scope, name and direction are required")
	}
	result, err := h.Traffic.GetTrafficCounter(ctx, instances.InstanceID(request.GetInstanceId()), selection)
	if mapped := grpcresponse.ContextError(errors.Join(ctx.Err(), err)); mapped != nil {
		return nil, mapped
	}
	switch {
	case errors.Is(err, instances.ErrNotFound):
		return nil, status.Error(codes.NotFound, "Xray instance not found")
	case errors.Is(err, domain.ErrTrafficCounterNotFound):
		return nil, status.Error(codes.NotFound, "Xray traffic counter not found")
	case errors.Is(err, domain.ErrAPIDisabled):
		return nil, status.Error(codes.FailedPrecondition, "Xray API is not enabled")
	case errors.Is(err, domain.ErrTrafficUnsupported):
		return nil, status.Error(codes.FailedPrecondition, "Xray traffic counters are not supported")
	case errors.Is(err, domain.ErrAPIBusy), errors.Is(err, domain.ErrAPIResourceLimit):
		return nil, status.Error(codes.ResourceExhausted, "Xray API resource limit exceeded")
	case err != nil:
		return nil, status.Error(codes.Unavailable, "cannot read Xray traffic counter")
	}
	return &xrayv1.GetTrafficCounterResponse{
		InstanceId: string(result.InstanceID), Scope: request.Scope, Name: result.Selection.Name,
		Direction: request.Direction, Bytes: result.Counter.Bytes, ObservedAtUnixMs: result.Counter.ObservedAt.UnixMilli(),
	}, nil
}

func decodeTrafficScope(scope xrayv1.TrafficScope) domain.TrafficScope {
	switch scope {
	case xrayv1.TrafficScope_TRAFFIC_SCOPE_USER:
		return domain.UserTraffic
	case xrayv1.TrafficScope_TRAFFIC_SCOPE_INBOUND:
		return domain.InboundTraffic
	case xrayv1.TrafficScope_TRAFFIC_SCOPE_OUTBOUND:
		return domain.OutboundTraffic
	default:
		return ""
	}
}
