package grpcapi

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcidentity"
	"github.com/PapilioVPN/node/internal/shared/grpcresponse"
)

type RuntimeInfoReader interface {
	GetRuntimeInfo(context.Context, instances.InstanceID) (domain.InstanceRuntimeInfo, error)
}

func (h Handler) GetRuntimeInfo(ctx context.Context, request *xrayv1.GetRuntimeInfoRequest) (*xrayv1.GetRuntimeInfoResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, err
	}
	if !h.AllowInfoRead {
		return nil, status.Error(codes.PermissionDenied, "engine information read is not granted")
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "instance_id is required")
	}
	result, err := h.Runtime.GetRuntimeInfo(ctx, instances.InstanceID(request.GetInstanceId()))
	if mapped := grpcresponse.ContextError(errors.Join(ctx.Err(), err)); mapped != nil {
		return nil, mapped
	}
	switch {
	case errors.Is(err, instances.ErrNotFound):
		return nil, status.Error(codes.NotFound, "Xray instance not found")
	case errors.Is(err, domain.ErrAPIDisabled):
		return nil, status.Error(codes.FailedPrecondition, "Xray API is not enabled")
	case errors.Is(err, domain.ErrRuntimeInfoUnsupported):
		return nil, status.Error(codes.FailedPrecondition, "Xray runtime information is not supported")
	case errors.Is(err, domain.ErrAPIBusy), errors.Is(err, domain.ErrAPIResourceLimit):
		return nil, status.Error(codes.ResourceExhausted, "Xray API resource limit exceeded")
	case err != nil:
		return nil, status.Error(codes.Unavailable, "cannot read Xray runtime information")
	}
	info := result.Runtime
	return &xrayv1.GetRuntimeInfoResponse{InstanceId: string(result.InstanceID), Runtime: &xrayv1.RuntimeInfo{
		Goroutines: info.Goroutines, GcCount: info.GCCount, AllocatedBytes: info.AllocatedBytes,
		TotalAllocatedBytes: info.TotalAllocatedBytes, SysBytes: info.SysBytes, Mallocs: info.Mallocs,
		Frees: info.Frees, LiveObjects: info.LiveObjects, TotalGcPauseNs: info.TotalGCPauseNS,
		UptimeSeconds: info.UptimeSeconds, ObservedAtUnixMs: info.ObservedAt.UnixMilli(),
	}}, nil
}
