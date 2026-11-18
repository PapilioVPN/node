// Package grpcapi adapts the host memory operation to its versioned gRPC API.
package grpcapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/memory/app/services"
	memoryv1 "github.com/PapilioVPN/node/internal/apps/system/host/memory/grpcapi/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	memoryv1.UnimplementedHostMemoryServiceServer
	Service services.Service
	Timeout time.Duration
}

func (handler Handler) Register(server grpc.ServiceRegistrar) {
	memoryv1.RegisterHostMemoryServiceServer(server, handler)
}

func (handler Handler) GetHostMemory(ctx context.Context, _ *memoryv1.GetHostMemoryRequest) (*memoryv1.GetHostMemoryResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.Read(ctx)
	if err != nil {
		return nil, readError(err)
	}
	return &memoryv1.GetHostMemoryResponse{
		TotalBytes:     result.Memory.TotalBytes,
		AvailableBytes: result.Memory.AvailableBytes,
		UsedBytes:      result.Memory.UsedBytes,
		FreeBytes:      result.Memory.FreeBytes,
		ActiveBytes:    result.Memory.ActiveBytes,
		InactiveBytes:  result.Memory.InactiveBytes,
		BuffersBytes:   result.Memory.BuffersBytes,
		CachedBytes:    result.Memory.CachedBytes,
		SharedBytes:    result.Memory.SharedBytes,
		SlabBytes:      result.Memory.SlabBytes,
		UsedPercent:    result.Memory.UsedPercent,
		ObservedAt:     timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration),
	}, nil
}

func (handler Handler) GetHostSwap(ctx context.Context, _ *memoryv1.GetHostSwapRequest) (*memoryv1.GetHostSwapResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.ReadSwap(ctx)
	if err != nil {
		return nil, readError(err)
	}
	return &memoryv1.GetHostSwapResponse{
		TotalBytes: result.Swap.TotalBytes, UsedBytes: result.Swap.UsedBytes, FreeBytes: result.Swap.FreeBytes,
		SwappedInBytes: result.Swap.SwappedInBytes, SwappedOutBytes: result.Swap.SwappedOutBytes, UsedPercent: result.Swap.UsedPercent,
		ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration),
	}, nil
}

func readError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Err()
	}
	return status.Error(codes.Unavailable, "host memory unavailable")
}
