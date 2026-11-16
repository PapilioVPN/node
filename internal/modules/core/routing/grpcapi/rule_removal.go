package grpcapi

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/core/routing/app/validators"
	"github.com/PapilioVPN/node/internal/modules/core/routing/domain"
	routingv1 "github.com/PapilioVPN/node/internal/modules/core/routing/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcidentity"
)

type RoutingRuleRemover interface {
	RemoveRoutingRule(context.Context, instances.InstanceID, string) (domain.InstanceRoutingRuleRemoval, error)
}

func (h RoutingAPI) RemoveRoutingRule(ctx context.Context, request *routingv1.RemoveRoutingRuleRequest) (*routingv1.RemoveRoutingRuleResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, routingMutationFailure(err, false)
	}
	if !h.AllowRoutingManagement {
		return nil, routingMutationFailure(status.Error(codes.PermissionDenied, "engine routing management is not granted"), false)
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" || validators.RoutingRuleTag(request.GetRuleTag()) != nil {
		return nil, routingMutationFailure(status.Error(codes.InvalidArgument, "instance_id and nonempty rule_tag are required"), false)
	}
	result, err := h.RuleRemoval.RemoveRoutingRule(ctx, instances.InstanceID(request.InstanceId), request.RuleTag)
	if err != nil {
		return nil, routingMutationError(ctx, err)
	}
	return &routingv1.RemoveRoutingRuleResponse{
		InstanceId: string(result.InstanceID), RuleTag: result.RuleTag,
		AcknowledgedAtUnixMs: result.Acknowledgement.AcknowledgedAt.UnixMilli(),
	}, nil
}
