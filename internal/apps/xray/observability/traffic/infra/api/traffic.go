// Package api adapts the pinned native targeted StatsService operation.
package api

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Reader struct{ client stats.StatsServiceClient }

// New borrows the instance-owned connection and constructs one reusable stub.
func New(connection grpc.ClientConnInterface) Reader {
	return Reader{client: stats.NewStatsServiceClient(connection)}
}

// Read performs exactly one GetStats(reset=false), without inventory or retries.
// Name construction matches the pinned dispatcher/proxyman counter registration.
func (r Reader) Read(ctx context.Context, selection domain.Selection) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	name := string(selection.Scope) + ">>>" + selection.Name + ">>>traffic>>>" + string(selection.Direction)
	result, err := r.client.GetStats(ctx, &stats.GetStatsRequest{Name: name, Reset_: false})
	if err != nil {
		return 0, nativeError(ctx, err)
	}
	if result == nil || result.Stat == nil || result.Stat.Name != name {
		return 0, domain.ErrUnavailable
	}
	return result.Stat.Value, nil
}

func nativeError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, rpc.ErrClosed):
		err = errors.Join(domain.ErrClosed, err)
	case status.Code(err) == codes.NotFound:
		err = errors.Join(domain.ErrCounterNotFound, err)
	case status.Code(err) == codes.Unimplemented:
		err = errors.Join(domain.ErrUnsupported, err)
	case status.Code(err) == codes.ResourceExhausted:
		err = errors.Join(domain.ErrCapacity, err)
	case status.Code(err) == codes.Canceled:
		err = errors.Join(context.Canceled, err)
	case status.Code(err) == codes.DeadlineExceeded:
		err = errors.Join(context.DeadlineExceeded, err)
	}
	return errors.Join(err, ctx.Err())
}
