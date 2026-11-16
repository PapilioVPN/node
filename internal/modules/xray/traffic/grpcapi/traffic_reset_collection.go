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

// TrafficCountersResetter retains its observed progress on failure as well as success.
type TrafficCountersResetter interface {
	ResetTrafficCounters(context.Context, instances.InstanceID, domain.TrafficScope, int) (domain.InstanceTrafficCountersReset, error)
}

func (h TrafficAPI) ResetTrafficCounters(ctx context.Context, request *trafficv1.ResetTrafficCountersRequest) (*trafficv1.ResetTrafficCountersResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, err
	}
	if !h.AllowStatsReset {
		return nil, status.Error(codes.PermissionDenied, "engine statistics reset is not granted")
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" || request.GetMaxCounters() == 0 {
		return nil, status.Error(codes.InvalidArgument, "instance_id and positive max_counters are required")
	}
	scope := decodeTrafficScope(request.GetScope())
	if err := validators.TrafficScope(scope); err != nil {
		return nil, status.Error(codes.InvalidArgument, "valid scope is required")
	}
	result, err := h.ScopeResets.ResetTrafficCounters(ctx, instances.InstanceID(request.InstanceId), scope, int(request.MaxCounters))
	response := encodeTrafficCountersReset(result, request.Scope)
	if err != nil {
		mapped := trafficResetStatus(ctx, err)
		detailed, detailError := mapped.WithDetails(&trafficv1.TrafficCountersResetFailure{Progress: response})
		if detailError != nil {
			return nil, mapped.Err()
		}
		return nil, detailed.Err()
	}
	// A confirmed final acknowledgement remains successful after late cancellation.
	return response, nil
}

func encodeTrafficCountersReset(result domain.InstanceTrafficCountersReset, scope trafficv1.TrafficScope) *trafficv1.ResetTrafficCountersResponse {
	response := &trafficv1.ResetTrafficCountersResponse{InstanceId: string(result.InstanceID), Scope: scope, Items: make([]*trafficv1.TrafficCounterResetEntry, len(result.Reset.Items))}
	if !result.Reset.SelectedAt.IsZero() {
		selectedAt := result.Reset.SelectedAt.UnixMilli()
		response.SelectedAtUnixMs = &selectedAt
	}
	for i, item := range result.Reset.Items {
		response.Items[i] = encodeTrafficResetEntry(item)
	}
	return response
}

func encodeTrafficResetEntry(item domain.TrafficCounterResetEntry) *trafficv1.TrafficCounterResetEntry {
	entry := &trafficv1.TrafficCounterResetEntry{Name: item.Name}
	switch item.Direction {
	case domain.Uplink:
		entry.Direction = trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK
	case domain.Downlink:
		entry.Direction = trafficv1.TrafficDirection_TRAFFIC_DIRECTION_DOWNLINK
	}
	switch item.State {
	case domain.TrafficResetNotAttempted:
		entry.State = trafficv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_NOT_ATTEMPTED
	case domain.TrafficResetAcknowledged:
		entry.State = trafficv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_ACKNOWLEDGED
	case domain.TrafficResetNotApplied:
		entry.State = trafficv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_NOT_APPLIED
	case domain.TrafficResetUnknown:
		entry.State = trafficv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_UNKNOWN
	case domain.TrafficResetMissing:
		entry.State = trafficv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_MISSING
	}
	if item.Reset != nil {
		entry.Acknowledgement = &trafficv1.TrafficCounterResetAcknowledgement{PreviousBytes: item.Reset.PreviousBytes, AcknowledgedAtUnixMs: item.Reset.AcknowledgedAt.UnixMilli()}
	}
	return entry
}
