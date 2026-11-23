package lifecycle

import (
	"context"

	handlercmd "github.com/xtls/xray-core/app/proxyman/command"
	routercmd "github.com/xtls/xray-core/app/router/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type configurationWriteKey struct{}

// serializeRoutingWrites prevents a public routing edit from interleaving with
// a multi-step configuration change on the same native connection.
func (run *execution) serializeRoutingWrites(ctx context.Context, method string, req, reply any, conn *grpc.ClientConn, invoke grpc.UnaryInvoker, options ...grpc.CallOption) error {
	switch method {
	case routercmd.RoutingService_AddRule_FullMethodName, routercmd.RoutingService_RemoveRule_FullMethodName,
		routercmd.RoutingService_OverrideBalancerTarget_FullMethodName,
		handlercmd.HandlerService_AddOutbound_FullMethodName, handlercmd.HandlerService_RemoveOutbound_FullMethodName:
	default:
		return invoke(ctx, method, req, reply, conn, options...)
	}
	if ctx.Value(configurationWriteKey{}) == run {
		return invoke(ctx, method, req, reply, conn, options...)
	}
	if err := run.routingWrites.Acquire(ctx, 1); err != nil {
		return status.FromContextError(err).Err()
	}
	defer run.routingWrites.Release(1)
	err := invoke(ctx, method, req, reply, conn, options...)
	switch status.Code(err) {
	case codes.Canceled, codes.DeadlineExceeded, codes.Unavailable:
		// A direct mutation may still be completing after its caller exited.
		// A later configuration change then needs a fresh process.
		run.routingUncertain = true
	}
	return err
}
