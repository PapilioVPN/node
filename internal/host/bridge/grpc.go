package bridge

import (
	"context"

	bridgegrpc "github.com/PapilioVPN/go-bridge/api/grpc/server"
	nodev1 "github.com/PapilioVPN/node/api/v1"
	"github.com/PapilioVPN/node/internal/host/services"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func registerGRPC(server *grpc.Server, sampler *services.CPUSampler) error {
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
	if err := bridgegrpc.HandleUnary(router, "GetCPU", getCPU(sampler)); err != nil {
		return err
	}
	return bridgegrpc.Setup(server, router)
}

func getCPU(sampler *services.CPUSampler) func(context.Context, *emptypb.Empty) (*nodev1.CPUSnapshot, error) {
	return func(_ context.Context, _ *emptypb.Empty) (*nodev1.CPUSnapshot, error) {
		sample, ok := sampler.Latest()
		if !ok {
			return nil, status.Error(codes.Unavailable, "host CPU sample is unavailable")
		}
		snapshot := &nodev1.CPUSnapshot{
			BusyPercent:        sample.BusyPercent,
			WindowMilliseconds: uint64(sample.Window.Milliseconds()),
			ObservedAt:         timestamppb.New(sample.ObservedAt),
		}
		if sample.HasSteal {
			snapshot.StealPercent = &sample.StealPercent
		}
		return snapshot, nil
	}
}

func getUptime(ctx context.Context, _ *emptypb.Empty) (*wrapperspb.UInt64Value, error) {
	seconds, err := services.Uptime(ctx)
	if err != nil {
		if cause := ctx.Err(); cause != nil {
			return nil, status.FromContextError(cause).Err()
		}
		return nil, status.Error(codes.Internal, "host uptime is unavailable")
	}
	return wrapperspb.UInt64(seconds), nil
}

func getMemory(ctx context.Context, _ *emptypb.Empty) (*nodev1.MemorySnapshot, error) {
	snapshot, err := services.Memory(ctx)
	if err != nil {
		if cause := ctx.Err(); cause != nil {
			return nil, status.FromContextError(cause).Err()
		}
		return nil, status.Error(codes.Internal, "host memory is unavailable")
	}
	return &nodev1.MemorySnapshot{
		TotalBytes:     snapshot.TotalBytes,
		AvailableBytes: snapshot.AvailableBytes,
		SwapTotalBytes: snapshot.SwapTotalBytes,
		SwapFreeBytes:  snapshot.SwapFreeBytes,
	}, nil
}
