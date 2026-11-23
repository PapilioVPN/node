// Package grpc exposes Xray routing reads over Node's gRPC API.
package grpc

import (
	"context"
	"errors"
	"net/netip"

	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	routingv1 "github.com/PapilioVPN/node/internal/engines/xray/routing/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/routing/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type handlers struct {
	routingv1.UnimplementedXrayRoutingServiceServer
	service *state.Service
}

func Register(server grpc.ServiceRegistrar, service *state.Service) {
	routingv1.RegisterXrayRoutingServiceServer(server, &handlers{service: service})
}

func (h *handlers) ListRoutingRules(ctx context.Context, req *routingv1.XrayRoutingRulesRequest) (*routingv1.XrayRoutingRules, error) {
	rules, err := h.service.ListRoutingRules(ctx, req.GetId())
	if err != nil {
		return nil, routingError(err)
	}

	items := make([]routingv1.XrayRoutingRule, len(rules))
	result := &routingv1.XrayRoutingRules{Rules: make([]*routingv1.XrayRoutingRule, len(rules))}
	for i, rule := range rules {
		items[i].RuleTag, items[i].OutboundTag = rule.RuleTag, rule.OutboundTag
		result.Rules[i] = &items[i]
	}
	return result, nil
}

func (h *handlers) GetBalancerInfo(ctx context.Context, req *routingv1.XrayBalancerInfoRequest) (*routingv1.XrayBalancerInfo, error) {
	info, err := h.service.GetBalancerInfo(ctx, req.GetId(), req.GetTag())
	if err != nil {
		return nil, routingError(err)
	}
	result := &routingv1.XrayBalancerInfo{OverrideTarget: info.OverrideTarget}
	if info.ReportedTargets != nil {
		result.ReportedTargets = &routingv1.XrayBalancerTargets{Tags: info.ReportedTargets}
	}
	return result, nil
}

func (h *handlers) TestRoute(ctx context.Context, req *routingv1.XrayTestRouteRequest) (*routingv1.XrayRouteSelection, error) {
	probe := state.RouteProbe{
		InboundTag: req.GetInboundTag(), Network: req.GetNetwork(), TargetDomain: req.GetTargetDomain(),
		TargetPort: req.GetTargetPort(), Protocol: req.GetProtocol(), User: req.GetUser(), Attributes: req.GetAttributes(),
	}
	if rawIP := req.GetTargetIp(); rawIP != "" {
		var err error
		probe.TargetIP, err = netip.ParseAddr(rawIP)
		if err != nil {
			return nil, routingError(state.ErrInvalid)
		}
	}
	selection, err := h.service.TestRoute(ctx, req.GetId(), probe)
	if err != nil {
		return nil, routingError(err)
	}
	return &routingv1.XrayRouteSelection{OutboundTag: selection.OutboundTag, OutboundGroupTags: selection.OutboundGroupTags}, nil
}

func (h *handlers) OverrideBalancerTarget(ctx context.Context, req *routingv1.XrayOverrideBalancerTargetRequest) (*emptypb.Empty, error) {
	if req == nil {
		return nil, routingError(state.ErrInvalid)
	}
	if err := h.service.OverrideBalancerTarget(ctx, req.GetId(), req.GetTag(), req.Target); err != nil {
		return nil, routingError(err)
	}
	return &emptypb.Empty{}, nil
}

func routingError(err error) error {
	switch {
	case errors.Is(err, lifecycle.ErrNotFound):
		return status.Error(codes.NotFound, lifecycle.ErrNotFound.Error())
	case errors.Is(err, state.ErrBalancerNotFound):
		return status.Error(codes.NotFound, state.ErrBalancerNotFound.Error())
	case errors.Is(err, state.ErrNoRoute):
		return status.Error(codes.NotFound, state.ErrNoRoute.Error())
	case errors.Is(err, state.ErrMutationUnknown):
		return status.Error(codes.Aborted, state.ErrMutationUnknown.Error())
	case errors.Is(err, state.ErrMutationFenced):
		return status.Error(codes.FailedPrecondition, state.ErrMutationFenced.Error())
	case errors.Is(err, state.ErrMutationBusy):
		return status.Error(codes.ResourceExhausted, state.ErrMutationBusy.Error())
	case errors.Is(err, state.ErrUnsupported):
		return status.Error(codes.Unimplemented, state.ErrUnsupported.Error())
	case errors.Is(err, state.ErrInvalid):
		return status.Error(codes.InvalidArgument, state.ErrInvalid.Error())
	case errors.Is(err, state.ErrBusy):
		return status.Error(codes.ResourceExhausted, state.ErrBusy.Error())
	case errors.Is(err, state.ErrTooLarge):
		return status.Error(codes.ResourceExhausted, state.ErrTooLarge.Error())
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.FromContextError(err).Err()
	default:
		return status.Error(codes.Unavailable, state.ErrUnavailable.Error())
	}
}
