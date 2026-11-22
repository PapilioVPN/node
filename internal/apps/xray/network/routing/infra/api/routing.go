// Package api adapts the pinned native RoutingService on a borrowed channel.
package api

import (
	"context"
	"errors"
	"net/netip"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	native "github.com/xtls/xray-core/app/router/command"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type Router struct{ client native.RoutingServiceClient }

func New(connection grpc.ClientConnInterface) *Router {
	return &Router{client: native.NewRoutingServiceClient(connection)}
}
func readError(err error) error {
	switch {
	case errors.Is(err, rpc.ErrClosed), status.Code(err) == codes.Unimplemented:
		return errors.Join(domain.ErrUnsupported, err)
	case status.Code(err) == codes.Canceled:
		return errors.Join(context.Canceled, err)
	case status.Code(err) == codes.DeadlineExceeded:
		return errors.Join(context.DeadlineExceeded, err)
	case status.Code(err) == codes.ResourceExhausted:
		return errors.Join(domain.ErrCapacity, err)
	}
	return err
}
func mutationResult(err error, remote peer.Peer, ctx context.Context) (domain.NativeResult, error) {
	if err == nil {
		return domain.NativeResult{Effect: domain.Completed, Acknowledged: true}, nil
	}
	if errors.Is(err, rpc.ErrClosed) || status.Code(err) == codes.Unimplemented {
		return domain.NativeResult{Effect: domain.NotApplied}, readError(err)
	}
	if remote.Addr == nil {
		return domain.NativeResult{Effect: domain.NotApplied}, errors.Join(readError(err), ctx.Err())
	}
	// Terminal ordinary native errors return Unknown. They can follow partial
	// work; transport/cancellation errors can also leave native work running.
	nativeStatus, terminal := status.FromError(err)
	unresolved := ctx.Err() != nil || !terminal || nativeStatus.Code() != codes.Unknown
	return domain.NativeResult{Effect: domain.Unknown, RequiresInstanceRestart: unresolved}, errors.Join(readError(err), ctx.Err())
}
func unknown(message proto.Message) bool { return len(message.ProtoReflect().GetUnknown()) != 0 }
func (r *Router) ListRules(ctx context.Context) ([]domain.Rule, error) {
	response, err := r.client.ListRule(ctx, &native.ListRuleRequest{}, grpc.MaxCallRecvMsgSize(domain.MaxNativeBytes))
	if err != nil {
		return nil, readError(err)
	}
	if response == nil || unknown(response) {
		return nil, domain.ErrInvalidResponse
	}
	if len(response.Rules) > domain.MaxRecords {
		return nil, domain.ErrCapacity
	}
	rows := make([]domain.Rule, len(response.Rules))
	for i, v := range response.Rules {
		if v == nil || unknown(v) {
			return nil, domain.ErrInvalidResponse
		}
		rows[i] = domain.Rule{OutboundTag: v.Tag, RuleTag: v.RuleTag}
	}
	return rows, nil
}
func (r *Router) Balancer(ctx context.Context, tag string) (domain.Balancer, error) {
	response, err := r.client.GetBalancerInfo(ctx, &native.GetBalancerInfoRequest{Tag: tag}, grpc.MaxCallRecvMsgSize(domain.MaxNativeBytes))
	if err != nil {
		return domain.Balancer{}, readError(err)
	}
	if response == nil || response.Balancer == nil || unknown(response) || unknown(response.Balancer) {
		return domain.Balancer{}, domain.ErrInvalidResponse
	}
	b := response.Balancer
	result := domain.Balancer{}
	if b.Override != nil {
		if unknown(b.Override) {
			return domain.Balancer{}, domain.ErrInvalidResponse
		}
		target := b.Override.Target
		result.Override = &target
	}
	if b.PrincipleTarget != nil {
		if unknown(b.PrincipleTarget) {
			return domain.Balancer{}, domain.ErrInvalidResponse
		}
		if len(b.PrincipleTarget.Tag) > domain.MaxPageSize {
			return domain.Balancer{}, domain.ErrCapacity
		}
		tags := append([]string{}, b.PrincipleTarget.Tag...)
		result.PrincipalTargets = &tags
	}
	return result, nil
}
func (r *Router) Add(ctx context.Context, data []byte, mode domain.Mode) (domain.NativeResult, error) {
	no := domain.NativeResult{Effect: domain.NotApplied}
	if err := ctx.Err(); err != nil {
		return no, err
	}
	if len(data) == 0 || len(data) > domain.MaxNativeBytes {
		return no, domain.ErrInvalidArgument
	}
	var remote peer.Peer
	_, err := r.client.AddRule(ctx, &native.AddRuleRequest{Config: &serial.TypedMessage{Type: "xray.app.router.Config", Value: data}, ShouldAppend: mode == domain.Append}, grpc.Peer(&remote))
	return mutationResult(err, remote, ctx)
}
func (r *Router) Remove(ctx context.Context, tag string) (domain.NativeResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.NativeResult{Effect: domain.NotApplied}, err
	}
	var remote peer.Peer
	_, err := r.client.RemoveRule(ctx, &native.RemoveRuleRequest{RuleTag: tag}, grpc.Peer(&remote))
	return mutationResult(err, remote, ctx)
}
func (r *Router) Override(ctx context.Context, tag, target string) (domain.NativeResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.NativeResult{Effect: domain.NotApplied}, err
	}
	var remote peer.Peer
	_, err := r.client.OverrideBalancerTarget(ctx, &native.OverrideBalancerTargetRequest{BalancerTag: tag, Target: target}, grpc.Peer(&remote))
	return mutationResult(err, remote, ctx)
}
func (r *Router) Probe(ctx context.Context, request domain.ProbeRequest) (domain.Context, domain.NativeResult, error) {
	no := domain.NativeResult{Effect: domain.NotApplied}
	if err := ctx.Err(); err != nil {
		return domain.Context{}, no, err
	}
	var remote peer.Peer
	response, err := r.client.TestRoute(ctx, &native.TestRouteRequest{RoutingContext: encodeContext(request.Context), FieldSelectors: request.FieldSelectors, PublishResult: request.PublishResult}, grpc.Peer(&remote), grpc.MaxCallRecvMsgSize(domain.MaxNativeBytes))
	result, err := mutationResult(err, remote, ctx)
	if err != nil {
		return domain.Context{}, result, err
	}
	route, err := decodeContext(response)
	if err != nil {
		return domain.Context{}, domain.NativeResult{Effect: domain.Unknown}, err
	}
	return route, result, nil
}
func encodeIPs(ips []netip.Addr) [][]byte {
	result := make([][]byte, len(ips))
	for i, ip := range ips {
		result[i] = ip.AsSlice()
	}
	return result
}
func encodeContext(c domain.Context) *native.RoutingContext {
	return &native.RoutingContext{InboundTag: c.InboundTag, Network: xnet.Network(c.Network), SourceIPs: encodeIPs(c.SourceIPs), TargetIPs: encodeIPs(c.TargetIPs), LocalIPs: encodeIPs(c.LocalIPs), SourcePort: uint32(c.SourcePort), TargetPort: uint32(c.TargetPort), LocalPort: uint32(c.LocalPort), VlessRoute: uint32(c.VLESSRoute), TargetDomain: c.TargetDomain, Protocol: c.Protocol, User: c.User, Attributes: c.Attributes, OutboundGroupTags: c.OutboundGroupTags, OutboundTag: c.OutboundTag}
}
func decodeIPs(ips [][]byte) ([]netip.Addr, error) {
	result := make([]netip.Addr, len(ips))
	for i, ip := range ips {
		v, ok := netip.AddrFromSlice(ip)
		if !ok {
			return nil, domain.ErrInvalidResponse
		}
		result[i] = v
	}
	return result, nil
}
func decodeContext(c *native.RoutingContext) (domain.Context, error) {
	if c == nil || unknown(c) || c.SourcePort > 65535 || c.TargetPort > 65535 || c.LocalPort > 65535 || c.VlessRoute > 65535 {
		return domain.Context{}, domain.ErrInvalidResponse
	}
	s, err := decodeIPs(c.SourceIPs)
	if err != nil {
		return domain.Context{}, err
	}
	t, err := decodeIPs(c.TargetIPs)
	if err != nil {
		return domain.Context{}, err
	}
	l, err := decodeIPs(c.LocalIPs)
	if err != nil {
		return domain.Context{}, err
	}
	result := domain.Context{InboundTag: c.InboundTag, Network: domain.Network(c.Network), SourceIPs: s, TargetIPs: t, LocalIPs: l, SourcePort: uint16(c.SourcePort), TargetPort: uint16(c.TargetPort), LocalPort: uint16(c.LocalPort), VLESSRoute: uint16(c.VlessRoute), TargetDomain: c.TargetDomain, Protocol: c.Protocol, User: c.User, Attributes: c.Attributes, OutboundGroupTags: append([]string{}, c.OutboundGroupTags...), OutboundTag: c.OutboundTag}
	if result.Network != domain.UnknownNetwork && result.Network != domain.TCP && result.Network != domain.UDP && result.Network != domain.UNIX {
		return domain.Context{}, domain.ErrInvalidResponse
	}
	return result, nil
}
