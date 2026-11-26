// Package grpc exposes candidate config preflight on Node's gRPC API.
package grpc

//go:generate protoc -I . --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative proto/v1/hysteria_configs.proto

import (
	"context"

	v1 "github.com/PapilioVPN/node/internal/engines/hysteria/configs/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/hysteria/configs/preflight"
	"github.com/PapilioVPN/node/internal/engines/hysteria/configs/validation"
	"github.com/PapilioVPN/node/internal/engines/hysteria/internal/grpcapi"
	"google.golang.org/grpc"
)

func Register(registrar grpc.ServiceRegistrar, service *validation.Service) {
	v1.RegisterHysteriaConfigsServiceServer(registrar, &handler{service: service})
}

type handler struct {
	v1.UnimplementedHysteriaConfigsServiceServer
	service *validation.Service
}

func (h *handler) ValidateConfig(ctx context.Context, r *v1.ValidateHysteriaConfigRequest) (*v1.HysteriaConfigValidation, error) {
	result, err := h.service.ValidateConfig(ctx, validation.Request{ID: r.GetId(), Source: r.GetConfigYaml()})
	if err != nil {
		return nil, grpcapi.ErrorStatus(err).Err()
	}
	return &v1.HysteriaConfigValidation{Id: result.ID, Valid: result.Issue == "", Scope: preflight.ValidationScope, Issue: string(result.Issue)}, nil
}
