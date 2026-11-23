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

func registerGRPC(server *grpc.Server, sampler *services.CPUSampler, storagePath string) error {
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
	if err := bridgegrpc.HandleUnary(router, "GetStorage", getStorage(storagePath)); err != nil {
		return err
	}
	if err := bridgegrpc.HandleUnary(router, "GetSystemStats", getSystemStats(sampler, storagePath)); err != nil {
		return err
	}
	return bridgegrpc.Setup(server, router)
}

func getStorage(path string) func(context.Context, *emptypb.Empty) (*nodev1.StorageSnapshot, error) {
	return func(ctx context.Context, _ *emptypb.Empty) (*nodev1.StorageSnapshot, error) {
		snapshot, err := services.Storage(ctx, path)
		if err != nil {
			if cause := ctx.Err(); cause != nil {
				return nil, status.FromContextError(cause).Err()
			}
			return nil, status.Error(codes.Unavailable, "host storage is unavailable")
		}
		return storageMessage(snapshot), nil
	}
}

func getCPU(sampler *services.CPUSampler) func(context.Context, *emptypb.Empty) (*nodev1.CPUSnapshot, error) {
	return func(_ context.Context, _ *emptypb.Empty) (*nodev1.CPUSnapshot, error) {
		sample, ok := sampler.Latest()
		if !ok {
			return nil, status.Error(codes.Unavailable, "host CPU sample is unavailable")
		}
		return cpuMessage(sample), nil
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
	return memoryMessage(snapshot), nil
}

func getSystemStats(sampler *services.CPUSampler, storagePath string) func(context.Context, *emptypb.Empty) (*nodev1.SystemSnapshot, error) {
	return func(ctx context.Context, _ *emptypb.Empty) (*nodev1.SystemSnapshot, error) {
		snapshot, err := services.SystemStats(ctx, sampler, storagePath)
		if err != nil {
			if cause := ctx.Err(); cause != nil {
				return nil, status.FromContextError(cause).Err()
			}
			return nil, status.Error(codes.Unavailable, "host system stats are unavailable")
		}
		response := &nodev1.SystemSnapshot{UptimeSeconds: snapshot.UptimeSeconds}
		if snapshot.CPU != nil {
			response.Cpu = cpuMessage(*snapshot.CPU)
		}
		if snapshot.Memory != nil {
			response.Memory = memoryMessage(*snapshot.Memory)
		}
		if snapshot.Storage != nil {
			response.Storage = storageMessage(*snapshot.Storage)
		}
		return response, nil
	}
}

func cpuMessage(sample services.CPUSample) *nodev1.CPUSnapshot {
	message := &nodev1.CPUSnapshot{
		BusyPercent:        sample.BusyPercent,
		WindowMilliseconds: uint64(sample.Window.Milliseconds()),
		ObservedAt:         timestamppb.New(sample.ObservedAt),
	}
	if sample.HasSteal {
		message.StealPercent = &sample.StealPercent
	}
	return message
}

func memoryMessage(snapshot services.MemorySnapshot) *nodev1.MemorySnapshot {
	return &nodev1.MemorySnapshot{
		TotalBytes:     snapshot.TotalBytes,
		AvailableBytes: snapshot.AvailableBytes,
		SwapTotalBytes: snapshot.SwapTotalBytes,
		SwapFreeBytes:  snapshot.SwapFreeBytes,
	}
}

func storageMessage(snapshot services.StorageSnapshot) *nodev1.StorageSnapshot {
	return &nodev1.StorageSnapshot{
		Path:           snapshot.Path,
		TotalBytes:     snapshot.TotalBytes,
		UsedBytes:      snapshot.UsedBytes,
		AvailableBytes: snapshot.AvailableBytes,
		ReadOnly:       snapshot.ReadOnly,
	}
}
