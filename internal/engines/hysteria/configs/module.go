// Package configs composes Hysteria's candidate configuration preflight.
package configs

import (
	grpcapi "github.com/PapilioVPN/node/internal/engines/hysteria/configs/bridge/grpc"
	httpapi "github.com/PapilioVPN/node/internal/engines/hysteria/configs/bridge/http"
	"github.com/PapilioVPN/node/internal/engines/hysteria/configs/preflight"
	"github.com/PapilioVPN/node/internal/engines/hysteria/configs/validation"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
)

type Capability struct{ service *validation.Service }

func New(files map[string]string) Capability {
	sources := make(map[string]preflight.Source, len(files))
	for id, file := range files {
		sources[id] = preflight.Source{Path: file}
	}
	return Capability{validation.New(sources)}
}
func (c Capability) RegisterHTTP(router chi.Router) { httpapi.Register(router, c.service) }
func (c Capability) RegisterGRPC(registrar grpc.ServiceRegistrar) {
	grpcapi.Register(registrar, c.service)
}
