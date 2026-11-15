package grpc

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/apps/system/domain"
	nodev1 "github.com/PapilioVPN/node/internal/entrypoints/grpc/proto/v1"
)

// NodeInfoReader is the application operation consumed by this adapter.
type NodeInfoReader interface {
	GetNodeInfo(context.Context) (domain.NodeInfo, error)
}

type Handler struct {
	nodev1.UnimplementedNodeServiceServer
	Info NodeInfoReader
}

func (h Handler) GetNodeInfo(ctx context.Context, _ *nodev1.GetNodeInfoRequest) (*nodev1.GetNodeInfoResponse, error) {
	info, err := h.Info.GetNodeInfo(ctx)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, status.FromContextError(err).Err()
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot read node information")
	}
	capabilities := make([]string, len(info.Capabilities))
	for i, value := range info.Capabilities {
		capabilities[i] = string(value)
	}
	return &nodev1.GetNodeInfoResponse{
		NodeId: string(info.NodeID), Version: info.Version,
		ApiVersion: info.APIVersion, Capabilities: capabilities,
	}, nil
}
