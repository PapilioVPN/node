package grpc

import (
	"context"

	hostv1 "github.com/PapilioVPN/node/internal/host/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/host/cpu"
	"github.com/PapilioVPN/node/internal/host/memory"
	"github.com/PapilioVPN/node/internal/host/network"
	"github.com/PapilioVPN/node/internal/host/storage"
	"github.com/PapilioVPN/node/internal/host/system"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func getStorage(path string) func(context.Context, *emptypb.Empty) (*hostv1.StorageSnapshot, error) {
	return func(ctx context.Context, _ *emptypb.Empty) (*hostv1.StorageSnapshot, error) {
		snapshot, err := storage.Read(ctx, path)
		if err != nil {
			if cause := ctx.Err(); cause != nil {
				return nil, status.FromContextError(cause).Err()
			}
			return nil, status.Error(codes.Unavailable, "host storage is unavailable")
		}
		return storageMessage(snapshot), nil
	}
}

func getCPU(sampler *cpu.Sampler) func(context.Context, *emptypb.Empty) (*hostv1.CPUSnapshot, error) {
	return func(_ context.Context, _ *emptypb.Empty) (*hostv1.CPUSnapshot, error) {
		sample, ok := sampler.Latest()
		if !ok {
			return nil, status.Error(codes.Unavailable, "host CPU sample is unavailable")
		}
		return cpuMessage(sample), nil
	}
}

func getUptime(ctx context.Context, _ *emptypb.Empty) (*wrapperspb.UInt64Value, error) {
	seconds, err := system.Uptime(ctx)
	if err != nil {
		if cause := ctx.Err(); cause != nil {
			return nil, status.FromContextError(cause).Err()
		}
		return nil, status.Error(codes.Internal, "host uptime is unavailable")
	}
	return wrapperspb.UInt64(seconds), nil
}

func getMemory(ctx context.Context, _ *emptypb.Empty) (*hostv1.MemorySnapshot, error) {
	snapshot, err := memory.Read(ctx)
	if err != nil {
		if cause := ctx.Err(); cause != nil {
			return nil, status.FromContextError(cause).Err()
		}
		return nil, status.Error(codes.Internal, "host memory is unavailable")
	}
	return memoryMessage(snapshot), nil
}

func getSystemStats(sampler *cpu.Sampler, netSampler *network.Sampler, storagePath string) func(context.Context, *emptypb.Empty) (*hostv1.SystemSnapshot, error) {
	return func(ctx context.Context, _ *emptypb.Empty) (*hostv1.SystemSnapshot, error) {
		snapshot, err := system.Stats(ctx, sampler, netSampler, storagePath)
		if err != nil {
			if cause := ctx.Err(); cause != nil {
				return nil, status.FromContextError(cause).Err()
			}
			return nil, status.Error(codes.Unavailable, "host system stats are unavailable")
		}
		return systemMessage(snapshot), nil
	}
}
