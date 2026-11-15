package grpcapi

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/app/validators"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcidentity"
)

type TrafficPairReader interface {
	GetTrafficPair(context.Context, instances.InstanceID, domain.TrafficPairSelection) (domain.InstanceTrafficPair, error)
}

func (h Handler) GetTrafficPair(ctx context.Context, request *xrayv1.GetTrafficPairRequest) (*xrayv1.GetTrafficPairResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, err
	}
	if !h.AllowStatsRead {
		return nil, status.Error(codes.PermissionDenied, "engine statistics read is not granted")
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "instance_id is required")
	}
	selection := domain.TrafficPairSelection{Scope: decodeTrafficScope(request.GetScope()), Name: request.GetName()}
	if err := validators.TrafficPairSelection(selection); err != nil {
		return nil, status.Error(codes.InvalidArgument, "valid scope and name are required")
	}
	result, err := h.Pairs.GetTrafficPair(ctx, instances.InstanceID(request.InstanceId), selection)
	if mapped := trafficReadError(ctx, err); mapped != nil {
		return nil, mapped
	}
	return &xrayv1.GetTrafficPairResponse{
		InstanceId: string(result.InstanceID), Scope: request.Scope, Name: result.Selection.Name,
		Uplink: encodeTrafficObservation(result.Pair.Uplink), Downlink: encodeTrafficObservation(result.Pair.Downlink),
	}, nil
}

func encodeTrafficObservation(counter *domain.TrafficCounter) *xrayv1.TrafficCounterObservation {
	if counter == nil {
		return nil
	}
	return &xrayv1.TrafficCounterObservation{Bytes: counter.Bytes, ObservedAtUnixMs: counter.ObservedAt.UnixMilli()}
}
