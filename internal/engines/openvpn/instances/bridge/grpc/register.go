// Package grpc exposes the OpenVPN instance API over gRPC.
package grpc

//go:generate protoc -I . --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative proto/v1/openvpn_instances.proto

import (
	pb "github.com/PapilioVPN/node/internal/engines/openvpn/instances/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/openvpn/instances/lifecycle"
	"google.golang.org/grpc"
)

func Register(server grpc.ServiceRegistrar, service *lifecycle.Service) {
	handler := &handlers{service: service}
	pb.RegisterOpenVPNInstancesServiceServer(server, handler)
	pb.RegisterOpenVPNConfigsServiceServer(server, handler)
	pb.RegisterOpenVPNOperationsServiceServer(server, handler)
	pb.RegisterOpenVPNAccessServiceServer(server, handler)
}
