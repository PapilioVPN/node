package grpc

import (
	"context"

	configsv1 "github.com/PapilioVPN/node/internal/engines/xray/configs/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/configs/validation"
)

type handlers struct {
	configsv1.UnimplementedXrayConfigsServiceServer
	service *validation.Service
}

func (h *handlers) ValidateConfig(ctx context.Context, request *configsv1.ValidateXrayConfigRequest) (*configsv1.XrayConfigValidation, error) {
	return response(h.service.Validate(ctx, request.GetId()))
}
