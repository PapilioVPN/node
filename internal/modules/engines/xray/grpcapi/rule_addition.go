package grpcapi

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcidentity"
)

type RoutingRuleAdder interface {
	AddRoutingRule(context.Context, instances.InstanceID, domain.RoutingRuleAddition) (domain.InstanceRoutingRuleAddition, error)
}

func (h Handler) AddRoutingRule(ctx context.Context, request *xrayv1.AddRoutingRuleRequest) (*xrayv1.AddRoutingRuleResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, routingMutationFailure(err, false)
	}
	if !h.AllowRoutingManagement {
		return nil, routingMutationFailure(status.Error(codes.PermissionDenied, "engine routing management is not granted"), false)
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" || len(request.GetRuleJson()) == 0 {
		return nil, routingMutationFailure(status.Error(codes.InvalidArgument, "instance_id and rule_json are required"), false)
	}
	var mode domain.RoutingRuleMode
	switch request.Mode {
	case xrayv1.RoutingRuleMode_ROUTING_RULE_MODE_APPEND:
		mode = domain.AppendRoutingRule
	case xrayv1.RoutingRuleMode_ROUTING_RULE_MODE_REPLACE:
		mode = domain.ReplaceRoutingRules
	default:
		return nil, routingMutationFailure(status.Error(codes.InvalidArgument, "explicit append or replace mode is required"), false)
	}
	result, err := h.RuleAddition.AddRoutingRule(ctx, instances.InstanceID(request.InstanceId), domain.RoutingRuleAddition{JSON: request.RuleJson, Mode: mode})
	if err != nil {
		return nil, routingMutationError(ctx, err)
	}
	return &xrayv1.AddRoutingRuleResponse{
		InstanceId: string(result.InstanceID), RuleTag: result.RuleTag,
		Mode: xrayv1.RoutingRuleMode(result.Mode), AcknowledgedAtUnixMs: result.Acknowledgement.AcknowledgedAt.UnixMilli(),
	}, nil
}
