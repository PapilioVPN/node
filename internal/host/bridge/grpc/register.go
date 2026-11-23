// Package grpc exposes Host observations over gRPC.
package grpc

import (
	bridgegrpc "github.com/PapilioVPN/go-bridge/api/grpc/server"
	"github.com/PapilioVPN/node/internal/host/services"
	"google.golang.org/grpc"
)

func Register(server *grpc.Server, sampler *services.CPUSampler, storagePath string) error {
	router, err := bridgegrpc.NewRouter("papiliovpn.node.v1.HostService")
	if err != nil {
		return err
	}
	if err := bridgegrpc.HandleUnary(router, "GetUptime", getUptime); err != nil {
		return err
	}
	if err := bridgegrpc.HandleUnary(router, "GetMemory", getMemory); err != nil {
		return err
	}
	if err := bridgegrpc.HandleUnary(router, "GetCPU", getCPU(sampler)); err != nil {
		return err
	}
	if err := bridgegrpc.HandleUnary(router, "GetStorage", getStorage(storagePath)); err != nil {
		return err
	}
	if err := bridgegrpc.HandleUnary(router, "GetSystemStats", getSystemStats(sampler, storagePath)); err != nil {
		return err
	}
	return bridgegrpc.Setup(server, router)
}
