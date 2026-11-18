// Package grpcapi adapts host processor operations to its versioned gRPC API.
package grpcapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/app/services"
	cpuv1 "github.com/PapilioVPN/node/internal/apps/system/host/cpu/grpcapi/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	cpuv1.UnimplementedHostLoadServiceServer
	cpuv1.UnimplementedHostCPUServiceServer
	Service services.Service
	Timeout time.Duration
}

func (handler Handler) Register(server grpc.ServiceRegistrar) {
	cpuv1.RegisterHostLoadServiceServer(server, handler)
	cpuv1.RegisterHostCPUServiceServer(server, handler)
}

func (handler Handler) GetHostLoad(ctx context.Context, _ *cpuv1.GetHostLoadRequest) (*cpuv1.GetHostLoadResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.Read(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, status.FromContextError(err).Err()
		}
		return nil, status.Error(codes.Unavailable, "host load unavailable")
	}
	return &cpuv1.GetHostLoadResponse{
		OneMinute: result.Averages.OneMinute, FiveMinutes: result.Averages.FiveMinutes, FifteenMinutes: result.Averages.FifteenMinutes,
		ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration),
	}, nil
}
