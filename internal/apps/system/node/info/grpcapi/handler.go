package grpcapi

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/apps/system/node/info/domain"
	systemv1 "github.com/PapilioVPN/node/internal/apps/system/node/info/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcresponse"
)

// NodeInfoReader is the application operation consumed by this adapter.
type NodeInfoReader interface {
	GetNodeInfo(context.Context) (domain.NodeInfo, error)
}

type Handler struct {
	systemv1.UnimplementedNodeServiceServer
	Info NodeInfoReader
}

func (h Handler) GetNodeInfo(ctx context.Context, _ *systemv1.GetNodeInfoRequest) (*systemv1.GetNodeInfoResponse, error) {
	info, err := h.Info.GetNodeInfo(ctx)
	if mapped := grpcresponse.ContextError(err); mapped != nil {
		return nil, mapped
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot read node information")
	}
	capabilities := make([]string, len(info.Capabilities))
	for i, value := range info.Capabilities {
		capabilities[i] = string(value)
	}
	return &systemv1.GetNodeInfoResponse{
		NodeId: string(info.NodeID), Version: info.Version,
		ApiVersion: info.APIVersion, Capabilities: capabilities,
	}, nil
}
