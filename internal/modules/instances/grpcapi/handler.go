// Package grpcapi adapts instance observation to the versioned gRPC contract.
package grpcapi

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	nodev1 "github.com/PapilioVPN/node/internal/entrypoints/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/modules/instances/domain"
)

type InstanceStatusReader interface {
	GetInstanceStatus(context.Context, domain.InstanceID) (domain.Status, error)
}

type Handler struct {
	nodev1.UnimplementedInstanceServiceServer
	Status          InstanceStatusReader
	AllowStatusRead bool
}

func (h Handler) GetInstanceStatus(ctx context.Context, request *nodev1.GetInstanceStatusRequest) (*nodev1.GetInstanceStatusResponse, error) {
	client, ok := peer.FromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "verified client certificate required")
	}
	identity, ok := client.AuthInfo.(credentials.TLSInfo)
	if !ok || len(identity.State.VerifiedChains) == 0 {
		return nil, status.Error(codes.Unauthenticated, "verified client certificate required")
	}
	if !h.AllowStatusRead {
		return nil, status.Error(codes.PermissionDenied, "instance status read is not granted")
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "instance_id is required")
	}
	value, err := h.Status.GetInstanceStatus(ctx, domain.InstanceID(request.GetInstanceId()))
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, status.FromContextError(err).Err()
	}
	if errors.Is(err, domain.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "instance not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot read instance status")
	}
	return statusResponse(value)
}
