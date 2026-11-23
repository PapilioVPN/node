// Package grpc exposes the Xray instance lifecycle over Node's gRPC API.
package grpc

import (
	instancesv1 "github.com/PapilioVPN/node/internal/engines/xray/instances/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	"google.golang.org/grpc"
)

func Register(registrar grpc.ServiceRegistrar, service *lifecycle.Service) {
	instancesv1.RegisterXrayInstancesServiceServer(registrar, &handlers{service: service})
}
