package grpcapi

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/traffic/app/validators"
	"github.com/PapilioVPN/node/internal/modules/xray/traffic/domain"
	trafficv1 "github.com/PapilioVPN/node/internal/modules/xray/traffic/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcidentity"
)

type TrafficPairResetter interface {
	ResetTrafficPair(context.Context, instances.InstanceID, domain.TrafficPairSelection) (domain.InstanceTrafficPairReset, error)
}

func (h TrafficAPI) ResetTrafficPair(ctx context.Context, request *trafficv1.ResetTrafficPairRequest) (*trafficv1.ResetTrafficPairResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, err
	}
	if !h.AllowStatsReset {
		return nil, status.Error(codes.PermissionDenied, "engine statistics reset is not granted")
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "instance_id is required")
	}
	selection := domain.TrafficPairSelection{Scope: decodeTrafficScope(request.GetScope()), Name: request.GetName()}
	if err := validators.TrafficPairSelection(selection); err != nil {
		return nil, status.Error(codes.InvalidArgument, "valid scope and name are required")
	}
	result, err := h.PairResets.ResetTrafficPair(ctx, instances.InstanceID(request.InstanceId), selection)
	response := &trafficv1.ResetTrafficPairResponse{InstanceId: string(result.InstanceID), Scope: request.Scope, Name: result.Selection.Name, Items: make([]*trafficv1.TrafficCounterResetEntry, len(result.Items))}
	for i, item := range result.Items {
		response.Items[i] = encodeTrafficResetEntry(item)
	}
	if err != nil {
		mapped := trafficResetStatus(ctx, err)
		detailed, detailError := mapped.WithDetails(&trafficv1.TrafficPairResetFailure{Progress: response})
		if detailError != nil {
			return nil, mapped.Err()
		}
		return nil, detailed.Err()
	}
	return response, nil
}
