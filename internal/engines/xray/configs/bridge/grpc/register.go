// Package grpc exposes Xray config validation over Node's gRPC API.
package grpc

import (
	configsv1 "github.com/PapilioVPN/node/internal/engines/xray/configs/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/configs/validation"
	"google.golang.org/grpc"
)

func Register(registrar grpc.ServiceRegistrar, service *validation.Service) {
	configsv1.RegisterXrayConfigsServiceServer(registrar, &handlers{service: service})
}
