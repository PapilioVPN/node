package grpc

import (
	"context"

	nodev1 "github.com/PapilioVPN/node/api/v1"
	"github.com/PapilioVPN/node/internal/host/services"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

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
		return systemMessage(snapshot), nil
	}
}
