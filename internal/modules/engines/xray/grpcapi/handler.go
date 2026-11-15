// Package grpcapi adapts Xray-specific operations to their versioned gRPC contract.
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

type BinaryInfoReader interface {
	GetBinaryInfo(context.Context, instances.InstanceID) (domain.InstanceBinaryInfo, error)
}

type Handler struct {
	xrayv1.UnimplementedXrayServiceServer
	Info                BinaryInfoReader
	Runtime             RuntimeInfoReader
	Traffic             TrafficCounterReader
	Resets              TrafficCounterResetter
	ScopeResets         TrafficCountersResetter
	Pairs               TrafficPairReader
	PairResets          TrafficPairResetter
	UserRemoval         InboundUserRemover
	UserAddition        InboundUserAdder
	TrafficCounters     TrafficCountersReader
	Online              OnlineReader
	AllowInfoRead       bool
	AllowStatsRead      bool
	AllowStatsReset     bool
	AllowOnlineRead     bool
	AllowUserManagement bool
}

func (h Handler) GetBinaryInfo(ctx context.Context, request *xrayv1.GetBinaryInfoRequest) (*xrayv1.GetBinaryInfoResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, err
	}
	if !h.AllowInfoRead {
		return nil, status.Error(codes.PermissionDenied, "engine information read is not granted")
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "instance_id is required")
	}
	result, err := h.Info.GetBinaryInfo(ctx, instances.InstanceID(request.GetInstanceId()))
	if mapped := grpcresponse.ContextError(errors.Join(ctx.Err(), err)); mapped != nil {
		return nil, mapped
	}
	switch {
	case errors.Is(err, instances.ErrNotFound):
		return nil, status.Error(codes.NotFound, "Xray instance not found")
	case errors.Is(err, domain.ErrInspectionDisabled):
		return nil, status.Error(codes.FailedPrecondition, "Xray binary inspection is not enabled")
	case errors.Is(err, domain.ErrInspectionBusy):
		return nil, status.Error(codes.ResourceExhausted, "Xray binary inspection is busy")
	case errors.Is(err, domain.ErrInfoOutputLimit):
		return nil, status.Error(codes.ResourceExhausted, "Xray binary information exceeds its output limit")
	case errors.Is(err, domain.ErrInvalidInfoOutput):
		return nil, status.Error(codes.FailedPrecondition, "unrecognized Xray binary information")
	case err != nil:
		return nil, status.Error(codes.Unavailable, "cannot read Xray binary information")
	}
	info := result.Binary
	return &xrayv1.GetBinaryInfoResponse{
		InstanceId: string(result.InstanceID),
		Binary: &xrayv1.BinaryInfo{
			Version: info.Version, Build: info.Build, GoVersion: info.GoVersion,
			OperatingSystem: info.OperatingSystem, Architecture: info.Architecture,
			ObservedAtUnixMs: info.ObservedAt.UnixMilli(),
		},
	}, nil
}
