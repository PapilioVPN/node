package grpcapi

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/core/traffic/app/validators"
	"github.com/PapilioVPN/node/internal/modules/core/traffic/domain"
	trafficv1 "github.com/PapilioVPN/node/internal/modules/core/traffic/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcidentity"
	"github.com/PapilioVPN/node/internal/shared/grpcresponse"
)

type TrafficCounterReader interface {
	GetTrafficCounter(context.Context, instances.InstanceID, domain.TrafficSelection) (domain.InstanceTrafficCounter, error)
}

func (h TrafficAPI) GetTrafficCounter(ctx context.Context, request *trafficv1.GetTrafficCounterRequest) (*trafficv1.GetTrafficCounterResponse, error) {
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
	case trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK:
		selection.Direction = domain.Uplink
	case trafficv1.TrafficDirection_TRAFFIC_DIRECTION_DOWNLINK:
		selection.Direction = domain.Downlink
	}
	if err := validators.TrafficSelection(selection); err != nil {
		return nil, status.Error(codes.InvalidArgument, "valid scope, name and direction are required")
	}
	result, err := h.Traffic.GetTrafficCounter(ctx, instances.InstanceID(request.GetInstanceId()), selection)
	if mapped := trafficReadError(ctx, err); mapped != nil {
		return nil, mapped
	}
	return &trafficv1.GetTrafficCounterResponse{
		InstanceId: string(result.InstanceID), Scope: request.Scope, Name: result.Selection.Name,
		Direction: request.Direction, Bytes: result.Counter.Bytes, ObservedAtUnixMs: result.Counter.ObservedAt.UnixMilli(),
	}, nil
}

func trafficReadError(ctx context.Context, err error) error {
	if mapped := grpcresponse.ContextError(errors.Join(ctx.Err(), err)); mapped != nil {
		return mapped
	}
	switch {
	case errors.Is(err, instances.ErrNotFound):
		return status.Error(codes.NotFound, "Xray instance not found")
	case errors.Is(err, domain.ErrTrafficCounterNotFound):
		return status.Error(codes.NotFound, "Xray traffic counter not found")
	case errors.Is(err, domain.ErrAPIDisabled):
		return status.Error(codes.FailedPrecondition, "Xray API is not enabled")
	case errors.Is(err, domain.ErrTrafficUnsupported):
		return status.Error(codes.FailedPrecondition, "Xray traffic counters are not supported")
	case errors.Is(err, domain.ErrAPIBusy), errors.Is(err, domain.ErrAPIResourceLimit):
		return status.Error(codes.ResourceExhausted, "Xray API resource limit exceeded")
	case err != nil:
		return status.Error(codes.Unavailable, "cannot read Xray traffic counter")
	}
	return nil
}

func decodeTrafficScope(scope trafficv1.TrafficScope) domain.TrafficScope {
	switch scope {
	case trafficv1.TrafficScope_TRAFFIC_SCOPE_USER:
		return domain.UserTraffic
	case trafficv1.TrafficScope_TRAFFIC_SCOPE_INBOUND:
		return domain.InboundTraffic
	case trafficv1.TrafficScope_TRAFFIC_SCOPE_OUTBOUND:
		return domain.OutboundTraffic
	default:
		return ""
	}
}
