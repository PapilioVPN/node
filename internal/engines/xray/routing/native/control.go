package native

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/engines/xray/routing/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	testRouteMethod = "/xray.app.router.command.RoutingService/TestRoute"
	overrideMethod  = "/xray.app.router.command.RoutingService/OverrideBalancerTarget"
)

var (
	probeOptions = []grpc.CallOption{grpc.StaticMethod(), grpc.MaxCallRecvMsgSize(64 << 10)}
	// A sent mutation is never eligible for grpc-go's replay buffer.
	mutationOptions = []grpc.CallOption{grpc.StaticMethod(), grpc.MaxCallRecvMsgSize(1024), grpc.MaxRetryRPCBufferSize(0)}
	probeFields     = []string{"outbound"}
)

func (c *Client) TestRoute(ctx context.Context, id string, probe state.RouteProbe) (state.RouteSelection, error) {
	var network int32
	switch probe.Network {
	case "tcp":
		network = 2
	case "udp":
		network = 3
	}
	input := &RouteContext{
		InboundTag: probe.InboundTag, Network: network, TargetPort: probe.TargetPort,
		TargetDomain: probe.TargetDomain, Protocol: probe.Protocol, User: probe.User,
		Attributes: probe.Attributes,
	}
	if probe.TargetIP.IsValid() {
		input.TargetIps = [][]byte{probe.TargetIP.Unmap().AsSlice()}
	}
	var result RouteContext
	err := c.invoke(ctx, id, testRouteMethod, &TestRouteRequest{RoutingContext: input, FieldSelectors: probeFields}, &result, probeOptions)
	if exactNativeError(err, "common: not enough information for making a decision") {
		return state.RouteSelection{}, errors.Join(state.ErrNoRoute, err)
	}
	if err != nil {
		return state.RouteSelection{}, err
	}
	return state.RouteSelection{OutboundTag: result.OutboundTag, OutboundGroupTags: result.OutboundGroupTags}, nil
}

func (c *Client) OverrideBalancerTarget(ctx context.Context, id, tag, target string) error {
	return c.mutate(ctx, id, overrideMethod, &OverrideBalancerTargetRequest{BalancerTag: tag, Target: target})
}

func (c *Client) mutate(ctx context.Context, id, method string, request any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !c.mutation.TryLock() {
		return state.ErrMutationBusy
	}
	defer c.mutation.Unlock()

	conn, err := c.instances.NativeConnection(id)
	if err != nil {
		return err
	}
	if c.fenced == conn {
		return state.ErrMutationFenced
	}
	c.fenced = nil
	if err := ctx.Err(); err != nil {
		return err
	}
	err = conn.Invoke(ctx, method, request, &emptypb.Empty{}, mutationOptions...)
	switch {
	case method == overrideMethod && exactNativeError(err, "app/router: cannot find tag"):
		return errors.Join(state.ErrBalancerNotFound, err)
	case exactNativeError(err, "app/router/command: unsupported router implementation"):
		return errors.Join(state.ErrUnsupported, err)
	case err != nil:
		c.fenced = conn
		return errors.Join(state.ErrMutationUnknown, err)
	}
	current, err := c.instances.NativeConnection(id)
	if err != nil || current != conn {
		return errors.Join(state.ErrMutationUnknown, err)
	}
	return nil
}

// The pinned router sends plain Go errors through gRPC Unknown.
func exactNativeError(err error, message string) bool {
	if err == nil {
		return false
	}
	nativeStatus, ok := status.FromError(err)
	return ok && nativeStatus.Code() == codes.Unknown && nativeStatus.Message() == message
}
