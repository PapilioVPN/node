// Package bridge exposes Host observations through the Node transports.
package bridge

import (
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
)

func Register(httpRouter *chi.Mux, grpcServer *grpc.Server) error {
	if err := registerGRPC(grpcServer); err != nil {
		return err
	}
	httpRouter.Get("/v1/host/uptime", handleUptime)
	httpRouter.Get("/v1/host/memory", handleMemory)
	return nil
}
