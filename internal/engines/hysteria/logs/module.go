// Package logs composes Hysteria's redacted execution log subscriptions.
package logs

import (
	"context"

	"github.com/PapilioVPN/node/internal/engines/hysteria/instances/lifecycle"
	grpcapi "github.com/PapilioVPN/node/internal/engines/hysteria/logs/bridge/grpc"
	httpapi "github.com/PapilioVPN/node/internal/engines/hysteria/logs/bridge/http"
	"github.com/PapilioVPN/node/internal/engines/hysteria/logs/live"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
)

type Capability struct{ service *live.Service }

func New(instances *lifecycle.Service) Capability {
	return Capability{live.New(func(ctx context.Context, id string) (*live.Journal, error) {
		e, err := instances.Current(ctx, id)
		if err != nil {
			return nil, err
		}
		return e.Run.Logs(), nil
	})}
}
func (c Capability) RegisterHTTP(router chi.Router) { httpapi.Register(router, c.service) }
func (c Capability) RegisterGRPC(registrar grpc.ServiceRegistrar) {
	grpcapi.Register(registrar, c.service)
}

func (c Capability) Close() { c.service.Close() }
