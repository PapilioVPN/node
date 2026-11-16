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

type BalancerOverrider interface {
	OverrideBalancerTarget(context.Context, instances.InstanceID, domain.BalancerOverride) (domain.InstanceBalancerOverride, error)
}

func (h Handler) OverrideBalancerTarget(ctx context.Context, request *xrayv1.OverrideBalancerTargetRequest) (*xrayv1.OverrideBalancerTargetResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, routingMutationFailure(err, false)
	}
	if !h.AllowRoutingManagement {
		return nil, routingMutationFailure(status.Error(codes.PermissionDenied, "engine routing management is not granted"), false)
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" || request.Target == nil {
		return nil, routingMutationFailure(status.Error(codes.InvalidArgument, "instance_id and explicit target are required"), false)
	}
	selection := domain.BalancerOverride{BalancerTag: request.BalancerTag, Target: *request.Target}
	if err := validators.BalancerOverride(selection); err != nil {
		return nil, routingMutationFailure(status.Error(codes.InvalidArgument, "valid balancer_tag and target are required"), false)
	}
	result, err := h.BalancerOverrides.OverrideBalancerTarget(ctx, instances.InstanceID(request.InstanceId), selection)
	if err != nil {
		return nil, routingMutationError(ctx, err)
	}
	// Keep confirmed completion after late cancellation; external delivery can fail.
	return &xrayv1.OverrideBalancerTargetResponse{
		InstanceId: string(result.InstanceID), BalancerTag: result.Override.BalancerTag,
		Target: result.Override.Target, AcknowledgedAtUnixMs: result.Acknowledgement.AcknowledgedAt.UnixMilli(),
	}, nil
}
