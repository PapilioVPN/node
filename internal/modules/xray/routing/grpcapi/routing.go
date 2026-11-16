package grpcapi

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/routing/app/validators"
	"github.com/PapilioVPN/node/internal/modules/xray/routing/domain"
	routingv1 "github.com/PapilioVPN/node/internal/modules/xray/routing/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcidentity"
	"github.com/PapilioVPN/node/internal/shared/grpcresponse"
)

type RoutingReader interface {
	ListRoutingRules(context.Context, instances.InstanceID) (domain.InstanceRoutingRules, error)
	GetBalancerInfo(context.Context, instances.InstanceID, string) (domain.InstanceBalancerInfo, error)
}

func (h RoutingAPI) ListRoutingRules(ctx context.Context, request *routingv1.ListRoutingRulesRequest) (*routingv1.ListRoutingRulesResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, err
	}
	if !h.AllowRoutingRead {
		return nil, status.Error(codes.PermissionDenied, "engine routing read is not granted")
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "instance_id is required")
	}
	result, err := h.Routing.ListRoutingRules(ctx, instances.InstanceID(request.InstanceId))
	if err := routingReadError(ctx, err); err != nil {
		return nil, err
	}
	rules := make([]*routingv1.RoutingRuleReference, len(result.Rules.Items))
	for i, rule := range result.Rules.Items {
		rules[i] = &routingv1.RoutingRuleReference{OutboundTag: rule.OutboundTag, RuleTag: rule.RuleTag}
	}
	return &routingv1.ListRoutingRulesResponse{
		InstanceId: string(result.InstanceID), Rules: rules, ObservedAtUnixMs: result.Rules.ObservedAt.UnixMilli(),
	}, nil
}

func (h RoutingAPI) GetBalancerInfo(ctx context.Context, request *routingv1.GetBalancerInfoRequest) (*routingv1.GetBalancerInfoResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, err
	}
	if !h.AllowRoutingRead {
		return nil, status.Error(codes.PermissionDenied, "engine routing read is not granted")
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" || validators.BalancerTag(request.GetBalancerTag()) != nil {
		return nil, status.Error(codes.InvalidArgument, "instance_id and balancer_tag are required")
	}
	result, err := h.Routing.GetBalancerInfo(ctx, instances.InstanceID(request.InstanceId), request.BalancerTag)
	if err := routingReadError(ctx, err); err != nil {
		return nil, err
	}
	response := &routingv1.GetBalancerInfoResponse{
		InstanceId: string(result.InstanceID), BalancerTag: result.Tag,
		OverrideTarget: result.Balancer.OverrideTarget, ObservedAtUnixMs: result.Balancer.ObservedAt.UnixMilli(),
	}
	if targets := result.Balancer.PrincipalTargets; targets != nil {
		response.PrincipalTargets = &routingv1.BalancerPrincipalTargets{Tags: targets.Tags}
	}
	return response, nil
}

func routingReadError(ctx context.Context, err error) error {
	if mapped := grpcresponse.ContextError(errors.Join(ctx.Err(), err)); mapped != nil {
		return mapped
	}
	switch {
	case errors.Is(err, instances.ErrNotFound):
		return status.Error(codes.NotFound, "Xray instance not found")
	case errors.Is(err, domain.ErrRoutingTargetNotFound):
		return status.Error(codes.NotFound, "Xray routing target not found")
	case errors.Is(err, domain.ErrAPIDisabled):
		return status.Error(codes.FailedPrecondition, "Xray API is not enabled")
	case errors.Is(err, domain.ErrRoutingReadUnsupported):
		return status.Error(codes.FailedPrecondition, "Xray routing observations are not supported")
	case errors.Is(err, domain.ErrAPIBusy), errors.Is(err, domain.ErrAPIResourceLimit):
		return status.Error(codes.ResourceExhausted, "Xray API resource limit exceeded")
	case err != nil:
		return status.Error(codes.Unavailable, "cannot read Xray routing information")
	default:
		return nil
	}
}
