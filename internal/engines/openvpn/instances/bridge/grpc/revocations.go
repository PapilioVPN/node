package grpc

import (
	"context"

	pb "github.com/PapilioVPN/node/internal/engines/openvpn/instances/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/openvpn/instances/lifecycle"
)

func (h *handlers) GetRevocationList(_ context.Context, req *pb.OpenVPNInstanceRequest) (*pb.OpenVPNRevocationStatus, error) {
	result, err := h.service.ReadRevocationList(req.GetId())
	return revocationResult(result, err)
}

func (h *handlers) ReplaceRevocationList(ctx context.Context, req *pb.ReplaceOpenVPNRevocationRequest) (*pb.OpenVPNRevocationStatus, error) {
	result, err := h.service.ReplaceRevocationList(ctx, req.GetId(), req.GetSignedPem())
	return revocationResult(result, err)
}

func revocationResult(result lifecycle.RevocationStatus, err error) (*pb.OpenVPNRevocationStatus, error) {
	if err != nil {
		return nil, rpcError(err).Err()
	}
	return &pb.OpenVPNRevocationStatus{
		Number: result.Number, Sha256: result.SHA256, NextUpdate: result.NextUpdate, RevokedCount: uint32(result.RevokedCount),
	}, nil
}
