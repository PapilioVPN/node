//go:build linux

package process

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// api is the process readiness consumer of the shared instance-owned channel.
type api struct {
	connection *rpc.Client
	stats      stats.StatsServiceClient
}

func newAPI(connection *rpc.Client) *api {
	resource := &api{connection: connection}
	if connection != nil {
		resource.stats = stats.NewStatsServiceClient(connection)
	}
	return resource
}
func (a *api) check(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if a.stats == nil {
		return false, domain.ErrUnsupported
	}
	_, err := a.stats.GetSysStats(ctx, &stats.SysStatsRequest{})
	switch {
	case errors.Is(err, rpc.ErrClosed):
		return false, errors.Join(domain.ErrClosed, err)
	case status.Code(err) == codes.Unimplemented:
		return false, errors.Join(domain.ErrUnsupported, err)
	case err != nil:
		return false, errors.Join(err, ctx.Err())
	default:
		return true, nil
	}
}

func (a *api) resetConnectBackoff() {
	if a.connection != nil {
		a.connection.ResetConnectBackoff()
	}
}

func (a *api) close() error {
	if a.connection == nil {
		return nil
	}
	return a.connection.Close()
}
