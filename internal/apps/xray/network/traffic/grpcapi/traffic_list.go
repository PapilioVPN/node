package grpcapi

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/network/traffic/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/network/traffic/domain"
	trafficv1 "github.com/PapilioVPN/node/internal/apps/xray/network/traffic/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcidentity"
	"github.com/PapilioVPN/node/internal/shared/grpcresponse"
)

type TrafficCountersReader interface {
	ListTrafficCounters(context.Context, instances.InstanceID, domain.TrafficScope) (domain.InstanceTrafficCounters, error)
}

func (h TrafficAPI) ListTrafficCounters(ctx context.Context, request *trafficv1.ListTrafficCountersRequest) (*trafficv1.ListTrafficCountersResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, err
	}
	if !h.AllowStatsRead {
		return nil, status.Error(codes.PermissionDenied, "engine statistics read is not granted")
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "instance_id is required")
	}
	scope := decodeTrafficScope(request.GetScope())
	if err := validators.TrafficScope(scope); err != nil {
		return nil, status.Error(codes.InvalidArgument, "valid scope is required")
	}
	result, err := h.TrafficCounters.ListTrafficCounters(ctx, instances.InstanceID(request.GetInstanceId()), scope)
	if mapped := grpcresponse.ContextError(errors.Join(ctx.Err(), err)); mapped != nil {
		return nil, mapped
	}
	switch {
	case errors.Is(err, instances.ErrNotFound):
		return nil, status.Error(codes.NotFound, "Xray instance not found")
	case errors.Is(err, domain.ErrAPIDisabled):
		return nil, status.Error(codes.FailedPrecondition, "Xray API is not enabled")
	case errors.Is(err, domain.ErrTrafficUnsupported):
		return nil, status.Error(codes.FailedPrecondition, "Xray traffic counters are not supported")
	case errors.Is(err, domain.ErrAPIBusy), errors.Is(err, domain.ErrAPIResourceLimit):
		return nil, status.Error(codes.ResourceExhausted, "Xray API resource limit exceeded")
	case err != nil:
		return nil, status.Error(codes.Unavailable, "cannot read Xray traffic counters")
	}
	items := make([]*trafficv1.TrafficCounterEntry, len(result.Counters.Items))
	for i, item := range result.Counters.Items {
		direction := trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UNSPECIFIED
		switch item.Direction {
		case domain.Uplink:
			direction = trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK
		case domain.Downlink:
			direction = trafficv1.TrafficDirection_TRAFFIC_DIRECTION_DOWNLINK
		}
		items[i] = &trafficv1.TrafficCounterEntry{Name: item.Name, Direction: direction, Bytes: item.Bytes}
	}
	if mapped := grpcresponse.ContextError(ctx.Err()); mapped != nil {
		return nil, mapped
	}
	return &trafficv1.ListTrafficCountersResponse{InstanceId: string(result.InstanceID), Scope: request.Scope, Items: items, ObservedAtUnixMs: result.Counters.ObservedAt.UnixMilli()}, nil
}
