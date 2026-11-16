package grpcapi

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcidentity"
	"github.com/PapilioVPN/node/internal/shared/grpcresponse"
)

type OutboundStatusReader interface {
	GetOutboundStatus(context.Context, instances.InstanceID, string) (domain.InstanceOutboundObservation, error)
	ListOutboundStatuses(context.Context, instances.InstanceID) (domain.InstanceOutboundStatuses, error)
}

func (h Handler) GetOutboundStatus(ctx context.Context, request *xrayv1.GetOutboundStatusRequest) (*xrayv1.GetOutboundStatusResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, err
	}
	if !h.AllowOutboundsRead {
		return nil, status.Error(codes.PermissionDenied, "engine outbound observations are not granted")
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" || request.OutboundTag == nil || !utf8.ValidString(request.GetOutboundTag()) {
		return nil, status.Error(codes.InvalidArgument, "instance_id and explicit outbound_tag are required")
	}
	result, err := h.Outbounds.GetOutboundStatus(ctx, instances.InstanceID(request.InstanceId), request.GetOutboundTag())
	if err := outboundReadError(ctx, err); err != nil {
		return nil, err
	}
	response := &xrayv1.GetOutboundStatusResponse{InstanceId: string(result.InstanceID), OutboundTag: result.Tag, ObservedAtUnixMs: result.Observation.ObservedAt.UnixMilli()}
	if value := result.Observation.Status; value != nil {
		response.Status = outboundStatusResponse(*value)
	}
	return response, nil
}

func (h Handler) ListOutboundStatuses(ctx context.Context, request *xrayv1.ListOutboundStatusesRequest) (*xrayv1.ListOutboundStatusesResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, err
	}
	if !h.AllowOutboundsRead {
		return nil, status.Error(codes.PermissionDenied, "engine outbound observations are not granted")
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "instance_id is required")
	}
	result, err := h.Outbounds.ListOutboundStatuses(ctx, instances.InstanceID(request.InstanceId))
	if err := outboundReadError(ctx, err); err != nil {
		return nil, err
	}
	values := make([]*xrayv1.OutboundStatus, len(result.Statuses.Items))
	for i, value := range result.Statuses.Items {
		if err := ctx.Err(); err != nil {
			return nil, outboundReadError(ctx, err)
		}
		values[i] = outboundStatusResponse(value)
	}
	if err := ctx.Err(); err != nil {
		return nil, outboundReadError(ctx, err)
	}
	return &xrayv1.ListOutboundStatusesResponse{InstanceId: string(result.InstanceID), Statuses: values, ObservedAtUnixMs: result.Statuses.ObservedAt.UnixMilli()}, nil
}

func outboundStatusResponse(value domain.OutboundStatus) *xrayv1.OutboundStatus {
	result := &xrayv1.OutboundStatus{OutboundTag: value.Tag, Alive: value.Alive, DelayMs: value.DelayMS, LastSeenUnixSec: value.LastSeenUnixSec, LastTryUnixSec: value.LastTryUnixSec}
	if h := value.HealthPing; h != nil {
		result.HealthPing = &xrayv1.OutboundHealthPing{TotalSamples: h.TotalSamples, FailedSamples: h.FailedSamples, DeviationNs: h.DeviationNS, AverageNs: h.AverageNS, MaxNs: h.MaxNS, MinNs: h.MinNS}
	}
	return result
}

func outboundReadError(ctx context.Context, err error) error {
	if mapped := grpcresponse.ContextError(errors.Join(ctx.Err(), err)); mapped != nil {
		return mapped
	}
	switch {
	case errors.Is(err, instances.ErrNotFound):
		return status.Error(codes.NotFound, "Xray instance not found")
	case errors.Is(err, domain.ErrAPIDisabled):
		return status.Error(codes.FailedPrecondition, "Xray API is not enabled")
	case errors.Is(err, domain.ErrOutboundObservationUnsupported):
		return status.Error(codes.FailedPrecondition, "Xray outbound observations are not supported")
	case errors.Is(err, domain.ErrAPIBusy), errors.Is(err, domain.ErrAPIResourceLimit):
		return status.Error(codes.ResourceExhausted, "Xray API resource limit exceeded")
	case err != nil:
		return status.Error(codes.Unavailable, "cannot read Xray outbound observations")
	default:
		return nil
	}
}
