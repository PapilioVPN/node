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
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, status.FromContextError(err).Err()
		}
		return nil, status.Error(codes.Unavailable, "host memory unavailable")
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
