package grpc

import (
	"context"

	statsv1 "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/online"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/runtime"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/traffic"
)

type handlers struct {
	statsv1.UnimplementedXrayStatsServiceServer
	service *runtime.Service
	traffic *traffic.Service
	online  *online.Service
}

func (h *handlers) GetRuntimeStats(ctx context.Context, req *statsv1.XrayRuntimeStatsRequest) (*statsv1.XrayRuntimeStats, error) {
	return response(h.service.GetRuntimeStats(ctx, req.GetId()))
}
