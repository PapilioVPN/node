package host

import (
	"context"

	bridgegrpc "github.com/PapilioVPN/go-bridge/api/grpc/server"
	nodev1 "github.com/PapilioVPN/node/api/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func registerGRPC(server *grpc.Server) error {
	router, err := bridgegrpc.NewRouter("papiliovpn.node.v1.HostService")
	if err != nil {
		return err
	}
	if err := bridgegrpc.HandleUnary(router, "GetUptime", getUptime); err != nil {
		return err
	}
	if err := bridgegrpc.HandleUnary(router, "GetMemory", getMemory); err != nil {
		return err
	}
	return bridgegrpc.Setup(server, router)
}

func getUptime(ctx context.Context, _ *emptypb.Empty) (*wrapperspb.UInt64Value, error) {
	seconds, err := readUptime(ctx)
	if err != nil {
		if cause := ctx.Err(); cause != nil {
			return nil, status.FromContextError(cause).Err()
		}
		return nil, status.Error(codes.Internal, "host uptime is unavailable")
	}
	return wrapperspb.UInt64(seconds), nil
}

func getMemory(ctx context.Context, _ *emptypb.Empty) (*nodev1.MemorySnapshot, error) {
	snapshot, err := readMemory(ctx)
	if err != nil {
		if cause := ctx.Err(); cause != nil {
			return nil, status.FromContextError(cause).Err()
		}
		return nil, status.Error(codes.Internal, "host memory is unavailable")
	}
	return &nodev1.MemorySnapshot{
		TotalBytes:     snapshot.totalBytes,
		AvailableBytes: snapshot.availableBytes,
		SwapTotalBytes: snapshot.swapTotalBytes,
		SwapFreeBytes:  snapshot.swapFreeBytes,
	}, nil
}
