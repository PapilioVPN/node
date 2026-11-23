// Package bridge exposes Host observations through the Node transports.
package bridge

import (
	"github.com/PapilioVPN/node/internal/host/services"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
)

func Register(httpRouter *chi.Mux, grpcServer *grpc.Server, sampler *services.CPUSampler, storagePath string) error {
	if err := registerGRPC(grpcServer, sampler, storagePath); err != nil {
		return err
	}
	httpRouter.Get("/v1/host/uptime", handleUptime)
	httpRouter.Get("/v1/host/memory", handleMemory)
	httpRouter.Get("/v1/host/cpu", handleCPU(sampler))
	httpRouter.Get("/v1/host/storage", handleStorage(storagePath))
	httpRouter.Get("/v1/host/stats", handleSystemStats(sampler, storagePath))
	return nil
}
