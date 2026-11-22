// Package grpcapi adapts routing operations to the versioned management service.
package grpcapi

import (
	"context"
	"net/netip"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/network/routing/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/infra/jsonwire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	v1.UnimplementedRoutingServiceServer
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(r grpc.ServiceRegistrar) { v1.RegisterRoutingServiceServer(r, h) }
func failure(err error) error {
	name := jsonwire.ErrorCode(err)
	code := codes.Unavailable
	switch name {
	case "invalid_argument":
		code = codes.InvalidArgument
	case "not_found":
		code = codes.NotFound
	case "resource_exhausted":
		code = codes.ResourceExhausted
	case "failed_precondition", "restart_required", "engine_unavailable", "observation_expired":
		code = codes.FailedPrecondition
	case "execution_changed":
		code = codes.Aborted
	case "canceled":
		code = codes.Canceled
	case "deadline_exceeded":
		code = codes.DeadlineExceeded
	}
	return status.Error(code, name)
}
func operation(o domain.Operation) *v1.RoutingOperation {
	r := &v1.RoutingOperation{InstanceId: o.InstanceID, Kind: o.Kind, Generation: o.Generation, Effect: string(o.Effect), Acknowledged: o.Acknowledged, RequiresInstanceRestart: o.RequiresInstanceRestart, DurationNs: int64(o.Duration)}
	if !o.ObservedAt.IsZero() {
		r.ObservedAt = timestamppb.New(o.ObservedAt)
	}
	return r
}
func mutation(o domain.Operation, err error) (*v1.RoutingOperation, error) {
	r := operation(o)
	if err == nil {
		return r, nil
	}
	detailed, e := status.Convert(failure(err)).WithDetails(r)
	if e != nil {
		return nil, failure(err)
	}
	return nil, detailed.Err()
}
func observation(o domain.Observation, source string) *v1.Observation {
	return &v1.Observation{InstanceId: o.InstanceID, Generation: o.Generation, ObservedAt: timestamppb.New(o.ObservedAt), DurationNs: int64(o.Duration), Source: source}
}
func (h Handler) ListRoutingRules(ctx context.Context, r *v1.ListRoutingRulesRequest) (*v1.RulePage, error) {
	if r == nil || r.PageSize > domain.MaxPageSize {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	p, err := h.Service.List(ctx, domain.PageRequest{InstanceID: r.InstanceId, PageSize: int(r.PageSize), PageToken: r.PageToken})
	if err != nil {
		return nil, failure(err)
	}
	result := &v1.RulePage{Observation: observation(p.Observation, "xray.routing.ListRule"), ObservationId: p.ObservationID, NextPageToken: p.NextPageToken, ExpiresAt: timestamppb.New(p.ExpiresAt), ServedAt: timestamppb.New(p.ServedAt), Total: uint32(p.Total), Fresh: p.Fresh}
	for _, rule := range p.Rules {
		result.Rules = append(result.Rules, &v1.RoutingRule{OutboundTag: rule.OutboundTag, RuleTag: rule.RuleTag})
	}
	return result, nil
}
func (h Handler) AddRoutingRule(ctx context.Context, r *v1.AddRoutingRuleRequest) (*v1.RoutingOperation, error) {
	if r == nil || (r.Mode != v1.RuleMode_RULE_MODE_APPEND && r.Mode != v1.RuleMode_RULE_MODE_REPLACE) {
		return mutation(domain.Operation{Kind: "add_rule", Effect: domain.NotApplied}, domain.ErrInvalidArgument)
	}
	mode := domain.Append
	if r.Mode == v1.RuleMode_RULE_MODE_REPLACE {
		mode = domain.Replace
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	o, err := h.Service.Add(ctx, domain.AddRequest{InstanceID: r.InstanceId, RuleJSON: r.RuleJson, Mode: mode, ConfirmDestructive: r.ConfirmDestructive})
	return mutation(o, err)
}
func (h Handler) RemoveRoutingRule(ctx context.Context, r *v1.RemoveRoutingRuleRequest) (*v1.RoutingOperation, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	o, err := h.Service.Remove(ctx, r.InstanceId, r.RuleTag)
	return mutation(o, err)
}
func (h Handler) OverrideBalancerTarget(ctx context.Context, r *v1.OverrideBalancerTargetRequest) (*v1.RoutingOperation, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	o, err := h.Service.Override(ctx, domain.OverrideRequest{InstanceID: r.InstanceId, BalancerTag: r.BalancerTag, Target: r.Target})
	return mutation(o, err)
}
func (h Handler) GetBalancerInfo(ctx context.Context, r *v1.GetBalancerInfoRequest) (*v1.BalancerInfo, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	b, err := h.Service.Balancer(ctx, r.InstanceId, r.BalancerTag)
	if err != nil {
		return nil, failure(err)
	}
	result := &v1.BalancerInfo{Observation: observation(b.Observation, "xray.routing.GetBalancerInfo"), BalancerTag: b.Tag, Override: b.Balancer.Override}
	if b.Balancer.PrincipalTargets != nil {
		result.PrincipalTargets = &v1.PrincipalTargets{Tags: *b.Balancer.PrincipalTargets}
	}
	return result, nil
}
func (h Handler) GetRoutingControlStatus(ctx context.Context, r *v1.InstanceRequest) (*v1.RoutingControlStatus, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	s, err := h.Service.Status(ctx, r.InstanceId)
	if err != nil {
		return nil, failure(err)
	}
	result := &v1.RoutingControlStatus{Observation: observation(s.Observation, "node.routing.control"), ExecutionState: s.ExecutionState, ApiConfigured: s.APIConfigured, RequestInFlight: s.RequestInFlight, RequiresInstanceRestart: s.RequiresInstanceRestart, RoutingStatsStream: s.RoutingStatsStream}
	if s.LastOperation != nil {
		result.LastOperation = operation(*s.LastOperation)
	}
	return result, nil
}
func parseIPs(raw []string) ([]netip.Addr, error) {
	out := make([]netip.Addr, len(raw))
	for i, s := range raw {
		a, err := netip.ParseAddr(s)
		if err != nil || a.Zone() != "" {
			return nil, domain.ErrInvalidArgument
		}
		out[i] = a
	}
	return out, nil
}
func contextInput(c *v1.RoutingContext) (domain.Context, error) {
	if c == nil || c.SourcePort > 65535 || c.TargetPort > 65535 || c.LocalPort > 65535 || c.VlessRoute > 65535 {
		return domain.Context{}, domain.ErrInvalidArgument
	}
	source, err := parseIPs(c.SourceIps)
	if err != nil {
		return domain.Context{}, err
	}
	target, err := parseIPs(c.TargetIps)
	if err != nil {
		return domain.Context{}, err
	}
	local, err := parseIPs(c.LocalIps)
	if err != nil {
		return domain.Context{}, err
	}
	return domain.Context{InboundTag: c.InboundTag, Network: domain.Network(c.Network), SourceIPs: source, TargetIPs: target, LocalIPs: local, SourcePort: uint16(c.SourcePort), TargetPort: uint16(c.TargetPort), LocalPort: uint16(c.LocalPort), VLESSRoute: uint16(c.VlessRoute), TargetDomain: c.TargetDomain, Protocol: c.Protocol, User: c.User, Attributes: c.Attributes, OutboundGroupTags: c.OutboundGroupTags, OutboundTag: c.OutboundTag}, nil
}
func ipsOutput(raw []netip.Addr) []string {
	out := make([]string, len(raw))
	for i, a := range raw {
		out[i] = a.String()
	}
	return out
}
func contextOutput(c domain.Context) *v1.RoutingContext {
	return &v1.RoutingContext{InboundTag: c.InboundTag, Network: v1.Network(c.Network), SourceIps: ipsOutput(c.SourceIPs), TargetIps: ipsOutput(c.TargetIPs), LocalIps: ipsOutput(c.LocalIPs), SourcePort: uint32(c.SourcePort), TargetPort: uint32(c.TargetPort), LocalPort: uint32(c.LocalPort), VlessRoute: uint32(c.VLESSRoute), TargetDomain: c.TargetDomain, Protocol: c.Protocol, User: c.User, Attributes: c.Attributes, OutboundGroupTags: c.OutboundGroupTags, OutboundTag: c.OutboundTag}
}
func (h Handler) TestRoute(ctx context.Context, r *v1.TestRouteRequest) (*v1.RouteResult, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	c, err := contextInput(r.Context)
	if err != nil {
		return nil, failure(err)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	p, err := h.Service.Probe(ctx, domain.ProbeRequest{InstanceID: r.InstanceId, Context: c, FieldSelectors: r.FieldSelectors, PublishResult: r.PublishResult})
	result := &v1.RouteResult{Operation: operation(p.Operation)}
	if err != nil {
		detailed, e := status.Convert(failure(err)).WithDetails(result.Operation)
		if e != nil {
			return nil, failure(err)
		}
		return nil, detailed.Err()
	}
	if p.Route != nil {
		result.Route = contextOutput(*p.Route)
	}
	return result, nil
}
