package grpc

import (
	"context"

	instancesv1 "github.com/PapilioVPN/node/internal/engines/xray/instances/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
)

type handlers struct {
	instancesv1.UnimplementedXrayInstancesServiceServer
	service *lifecycle.Service
}

func (h *handlers) GetStatus(_ context.Context, req *instancesv1.XrayInstanceRequest) (*instancesv1.XrayInstanceStatus, error) {
	return response(h.service.GetStatus(req.GetId()))
}

func (h *handlers) Start(ctx context.Context, req *instancesv1.XrayInstanceRequest) (*instancesv1.XrayInstanceStatus, error) {
	return response(h.service.Start(ctx, req.GetId()))
}

func (h *handlers) Stop(ctx context.Context, req *instancesv1.XrayInstanceRequest) (*instancesv1.XrayInstanceStatus, error) {
	return response(h.service.Stop(ctx, req.GetId()))
}
