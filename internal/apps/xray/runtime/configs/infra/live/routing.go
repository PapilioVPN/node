// Package live adapts existing native capability operations for config changes.
package live

import (
	"context"
	"errors"
	routing "github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
	routingapi "github.com/PapilioVPN/node/internal/apps/xray/network/routing/infra/api"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"google.golang.org/grpc"
)

type Routing struct{ router *routingapi.Router }

func New(connection grpc.ClientConnInterface) *Routing {
	return &Routing{router: routingapi.New(connection)}
}
func (r *Routing) ApplyRouting(ctx context.Context, payload []byte) (domain.LiveResult, error) {
	result, err := r.router.Add(ctx, payload, routing.Replace)
	switch {
	case errors.Is(err, routing.ErrInvalidArgument):
		err = errors.Join(domain.ErrInvalidArgument, err)
	case errors.Is(err, routing.ErrUnsupported):
		err = errors.Join(domain.ErrUnsupported, err)
	case errors.Is(err, routing.ErrCapacity):
		err = errors.Join(domain.ErrCapacity, err)
	}
	return domain.LiveResult{Effect: string(result.Effect), Acknowledged: result.Acknowledged}, err
}
