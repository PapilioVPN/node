// Package stats composes Hysteria's runtime and native traffic observations.
package stats

import (
	"context"

	"github.com/PapilioVPN/node/internal/engines/hysteria/instances/lifecycle"
	grpcapi "github.com/PapilioVPN/node/internal/engines/hysteria/stats/bridge/grpc"
	httpapi "github.com/PapilioVPN/node/internal/engines/hysteria/stats/bridge/http"
	"github.com/PapilioVPN/node/internal/engines/hysteria/stats/observations"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
)

type Capability struct{ service *observations.Service }

func New(instances *lifecycle.Service) Capability {
	return Capability{observations.New(func(ctx context.Context, id string) (observations.Execution, error) {
		e, err := instances.Current(ctx, id)
		if err != nil {
			return observations.Execution{}, err
		}
		return observations.Execution{Number: e.Number, ReadyAt: e.ReadyAt, Lifetime: e.Run.Context(), Native: e.Run.Stats()}, nil
	})}
}
func (c Capability) RegisterHTTP(router chi.Router) { httpapi.Register(router, c.service) }
func (c Capability) RegisterGRPC(registrar grpc.ServiceRegistrar) {
	grpcapi.Register(registrar, c.service)
}
