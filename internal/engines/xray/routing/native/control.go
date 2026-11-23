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
	var probeRequest struct {
		context RouteContext
		request TestRouteRequest
	}
	probeRequest.context = RouteContext{
		InboundTag: probe.InboundTag, Network: network, TargetPort: probe.TargetPort,
		TargetDomain: probe.TargetDomain, Protocol: probe.Protocol, User: probe.User,
		Attributes: probe.Attributes,
	}
	if probe.TargetIP.IsValid() {
		probeRequest.context.TargetIps = [][]byte{probe.TargetIP.Unmap().AsSlice()}
	}
	probeRequest.request.RoutingContext = &probeRequest.context
	probeRequest.request.FieldSelectors = probeFields
	var result RouteContext
	if err := c.invoke(ctx, id, testRouteMethod, &probeRequest.request, &result, probeOptions); err != nil {
		if exactNativeError(err, "common: not enough information for making a decision") {
			return state.RouteSelection{}, errors.Join(state.ErrNoRoute, err)
		}
		return state.RouteSelection{}, err
	}
	return state.RouteSelection{OutboundTag: result.OutboundTag, OutboundGroupTags: result.OutboundGroupTags}, nil
}

func (c *Client) OverrideBalancerTarget(ctx context.Context, id, tag, target string) error {
	request := &OverrideBalancerTargetRequest{BalancerTag: tag, Target: target}
	return c.mutate(ctx, id, overrideMethod, request, func(err error) error {
		if exactNativeError(err, "app/router: cannot find tag") {
			return state.ErrBalancerNotFound
		}
		return nil
	})
}

func (c *Client) mutate(ctx context.Context, id, method string, request any, rejected func(error) error) error {
	return c.withMutation(ctx, id, func(conn *grpc.ClientConn) error {
		return c.sendMutation(ctx, id, conn, method, request, rejected)
	})
}

func (c *Client) withMutation(ctx context.Context, id string, run func(*grpc.ClientConn) error) error {
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
	return run(conn)
}

func (c *Client) sendMutation(ctx context.Context, id string, conn *grpc.ClientConn, method string, request any, rejected func(error) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := conn.Invoke(localDeadlineContext{ctx}, method, request, &emptypb.Empty{}, mutationOptions...)
	if err != nil {
		if exactNativeError(err, "app/router/command: unsupported router implementation") ||
			exactNativeError(err, "common: not enough information for making a decision") {
			return errors.Join(state.ErrUnsupported, err)
		}
		if rejected != nil {
			if cause := rejected(err); cause != nil {
				return errors.Join(cause, err)
			}
		}
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
