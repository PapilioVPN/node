// Package grpcapi adapts the host uptime operation to its versioned gRPC API.
package grpcapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/uptime/app/services"
	uptimev1 "github.com/PapilioVPN/node/internal/apps/system/host/uptime/grpcapi/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	uptimev1.UnimplementedHostUptimeServiceServer
	Service services.Service
	Timeout time.Duration
}

func (handler Handler) Register(server grpc.ServiceRegistrar) {
	uptimev1.RegisterHostUptimeServiceServer(server, handler)
}

func (handler Handler) GetHostUptime(ctx context.Context, _ *uptimev1.GetHostUptimeRequest) (*uptimev1.GetHostUptimeResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.Read(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, status.FromContextError(err).Err()
		}
		return nil, status.Error(codes.Unavailable, "host uptime unavailable")
	}
	return &uptimev1.GetHostUptimeResponse{
		UptimeNs:   int64(result.Uptime),
		ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration),
	}, nil
}
