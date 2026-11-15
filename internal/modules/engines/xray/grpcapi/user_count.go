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

type InboundUserCountReader interface {
	GetInboundUserCount(context.Context, instances.InstanceID, string) (domain.InstanceInboundUserCount, error)
}

func (h Handler) GetInboundUserCount(ctx context.Context, request *xrayv1.GetInboundUserCountRequest) (*xrayv1.GetInboundUserCountResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, err
	}
	if !h.AllowUsersRead {
		return nil, status.Error(codes.PermissionDenied, "engine user read is not granted")
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" || validators.InboundTag(request.GetInboundTag()) != nil {
		return nil, status.Error(codes.InvalidArgument, "instance_id and inbound_tag are required")
	}
	result, err := h.UserCounts.GetInboundUserCount(ctx, instances.InstanceID(request.InstanceId), request.InboundTag)
	if mapped := grpcresponse.ContextError(errors.Join(ctx.Err(), err)); mapped != nil {
		return nil, mapped
	}
	switch {
	case errors.Is(err, instances.ErrNotFound):
		return nil, status.Error(codes.NotFound, "Xray instance not found")
	case errors.Is(err, domain.ErrInboundNotFound):
		return nil, status.Error(codes.NotFound, "Xray inbound not found")
	case errors.Is(err, domain.ErrAPIDisabled):
		return nil, status.Error(codes.FailedPrecondition, "Xray API is not enabled")
	case errors.Is(err, domain.ErrUserReadUnsupported):
		return nil, status.Error(codes.FailedPrecondition, "Xray user observations are not supported")
	case errors.Is(err, domain.ErrAPIBusy), errors.Is(err, domain.ErrAPIResourceLimit):
		return nil, status.Error(codes.ResourceExhausted, "Xray API resource limit exceeded")
	case err != nil:
		return nil, status.Error(codes.Unavailable, "cannot read Xray inbound user count")
	}
	return &xrayv1.GetInboundUserCountResponse{InstanceId: string(result.InstanceID), InboundTag: result.InboundTag, Count: result.Users.Count, ObservedAtUnixMs: result.Users.ObservedAt.UnixMilli()}, nil
}
