// Package grpcapi adapts the host identity operation to its versioned gRPC API.
package grpcapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/identity/app/services"
	identityv1 "github.com/PapilioVPN/node/internal/apps/system/host/identity/grpcapi/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	identityv1.UnimplementedHostIdentityServiceServer
	Service services.Service
	Timeout time.Duration
}

func (handler Handler) Register(server grpc.ServiceRegistrar) {
	identityv1.RegisterHostIdentityServiceServer(server, handler)
}

func (handler Handler) GetHostIdentity(ctx context.Context, _ *identityv1.GetHostIdentityRequest) (*identityv1.GetHostIdentityResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.Read(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, status.FromContextError(err).Err()
		}
		return nil, status.Error(codes.Unavailable, "host identity unavailable")
	}
	return &identityv1.GetHostIdentityResponse{
		Identity: &identityv1.HostIdentity{
			Hostname: result.Identity.Hostname, OperatingSystem: result.Identity.OperatingSystem,
			Release: result.Identity.Release, Version: result.Identity.Version, Machine: result.Identity.Machine,
		},
		ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration),
	}, nil
}
