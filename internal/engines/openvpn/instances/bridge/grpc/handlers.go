package grpc

import (
	"context"

	pb "github.com/PapilioVPN/node/internal/engines/openvpn/instances/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/openvpn/instances/lifecycle"
)

type handlers struct {
	pb.UnimplementedOpenVPNInstancesServiceServer
	pb.UnimplementedOpenVPNConfigsServiceServer
	pb.UnimplementedOpenVPNOperationsServiceServer
	pb.UnimplementedOpenVPNAccessServiceServer
	service *lifecycle.Service
}

func (h *handlers) Start(ctx context.Context, req *pb.OpenVPNStartRequest) (*pb.OpenVPNInstanceStatus, error) {
	status, err := h.service.Start(ctx, req.GetId(), req.ConfigText)
	return instanceResult(status, err)
}

func (h *handlers) GetStatus(_ context.Context, req *pb.OpenVPNInstanceRequest) (*pb.OpenVPNInstanceStatus, error) {
	status, err := h.service.GetStatus(req.GetId())
	return instanceResult(status, err)
}

func (h *handlers) Stop(ctx context.Context, req *pb.OpenVPNInstanceRequest) (*pb.OpenVPNInstanceStatus, error) {
	status, err := h.service.Stop(ctx, req.GetId())
	return instanceResult(status, err)
}

func (h *handlers) ValidateConfig(ctx context.Context, req *pb.ValidateOpenVPNConfigRequest) (*pb.OpenVPNConfigValidation, error) {
	result, err := h.service.Validate(ctx, req.GetId(), req.ConfigText)
	if err != nil {
		return nil, rpcError(err).Err()
	}
	response := &pb.OpenVPNConfigValidation{Id: result.ID, Valid: result.Issue.Code == ""}
	if !response.Valid {
		response.Issue = &pb.OpenVPNConfigIssue{Code: issueCode(result.Issue.Code), Line: result.Issue.Line}
	}
	return response, nil
}
