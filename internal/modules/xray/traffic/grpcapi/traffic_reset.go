package grpcapi

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/traffic/app/validators"
	"github.com/PapilioVPN/node/internal/modules/xray/traffic/domain"
	trafficv1 "github.com/PapilioVPN/node/internal/modules/xray/traffic/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcidentity"
	"github.com/PapilioVPN/node/internal/shared/grpcresponse"
)

type TrafficCounterResetter interface {
	ResetTrafficCounter(context.Context, instances.InstanceID, domain.TrafficSelection) (domain.InstanceTrafficCounterReset, error)
}

func (h TrafficAPI) ResetTrafficCounter(ctx context.Context, request *trafficv1.ResetTrafficCounterRequest) (*trafficv1.ResetTrafficCounterResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, err
	}
	if !h.AllowStatsReset {
		return nil, status.Error(codes.PermissionDenied, "engine statistics reset is not granted")
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "instance_id is required")
	}
	selection := domain.TrafficSelection{Scope: decodeTrafficScope(request.GetScope()), Name: request.GetName()}
	switch request.GetDirection() {
	case trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK:
		selection.Direction = domain.Uplink
	case trafficv1.TrafficDirection_TRAFFIC_DIRECTION_DOWNLINK:
		selection.Direction = domain.Downlink
	}
	if err := validators.TrafficSelection(selection); err != nil {
		return nil, status.Error(codes.InvalidArgument, "valid scope, name and direction are required")
	}
	result, err := h.Resets.ResetTrafficCounter(ctx, instances.InstanceID(request.GetInstanceId()), selection)
	if err != nil {
		return nil, trafficResetError(ctx, err)
	}
	// Retain a confirmed acknowledgement; the transport can still lose its delivery.
	return &trafficv1.ResetTrafficCounterResponse{
		InstanceId: string(result.InstanceID), Scope: request.Scope, Name: result.Selection.Name, Direction: request.Direction,
		PreviousBytes: result.Reset.PreviousBytes, AcknowledgedAtUnixMs: result.Reset.AcknowledgedAt.UnixMilli(),
	}, nil
}

func trafficResetError(ctx context.Context, cause error) error {
	mapped := trafficResetStatus(ctx, cause)
	outcome := trafficv1.TrafficCounterResetOutcome_TRAFFIC_COUNTER_RESET_OUTCOME_NOT_APPLIED
	if errors.Is(cause, domain.ErrTrafficResetUnknown) {
		outcome = trafficv1.TrafficCounterResetOutcome_TRAFFIC_COUNTER_RESET_OUTCOME_UNKNOWN
	}
	detailed, err := mapped.WithDetails(&trafficv1.TrafficCounterResetFailure{Outcome: outcome})
	if err != nil {
		// Missing details must never be treated as no effect.
		return mapped.Err()
	}
	return detailed.Err()
}

func trafficResetStatus(ctx context.Context, cause error) *status.Status {
	mapped := grpcresponse.ContextError(errors.Join(cause, ctx.Err()))
	if mapped == nil {
		code, message := codes.Unavailable, "cannot reset Xray traffic counter"
		switch {
		case errors.Is(cause, instances.ErrNotFound):
			code, message = codes.NotFound, "Xray instance not found"
		case errors.Is(cause, domain.ErrTrafficCounterNotFound):
			code, message = codes.NotFound, "Xray traffic counter not found"
		case errors.Is(cause, domain.ErrAPIDisabled):
			code, message = codes.FailedPrecondition, "Xray API is not enabled"
		case errors.Is(cause, domain.ErrTrafficUnsupported):
			code, message = codes.FailedPrecondition, "Xray traffic counters are not supported"
		case errors.Is(cause, domain.ErrAPIBusy), errors.Is(cause, domain.ErrAPIResourceLimit):
			code, message = codes.ResourceExhausted, "Xray API resource limit exceeded"
		}
		mapped = status.Error(code, message)
	}
	return status.Convert(mapped)
}
