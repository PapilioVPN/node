// Package host exposes observations from the local operating system.
package host

import (
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

var Module = fx.Module("host", fx.Invoke(register))

func register(httpRouter *chi.Mux, grpcServer *grpc.Server) error {
	if err := registerGRPC(grpcServer); err != nil {
		return err
	}
	httpRouter.Get("/v1/host/uptime", handleUptime)
	httpRouter.Get("/v1/host/memory", handleMemory)
	return nil
}
