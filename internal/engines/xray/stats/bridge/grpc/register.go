// Package grpc exposes Xray runtime statistics over Node's gRPC API.
package grpc

import (
	statsv1 "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/runtime"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/traffic"
	"google.golang.org/grpc"
)

func Register(registrar grpc.ServiceRegistrar, service *runtime.Service, trafficService *traffic.Service) {
	statsv1.RegisterXrayStatsServiceServer(registrar, &handlers{service: service, traffic: trafficService})
}
