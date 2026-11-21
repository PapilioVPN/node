// Package api reads Xray runtime facts through its pinned native StatsService.
package api

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/statistics/domain"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Reader struct{ client stats.StatsServiceClient }

func New(connection grpc.ClientConnInterface) Reader {
	return Reader{client: stats.NewStatsServiceClient(connection)}
}
func (r Reader) Read(ctx context.Context) (domain.Statistics, error) {
	result, err := r.client.GetSysStats(ctx, &stats.SysStatsRequest{})
	if err != nil {
		switch {
		case errors.Is(err, rpc.ErrClosed):
			err = errors.Join(domain.ErrClosed, err)
		case status.Code(err) == codes.Unimplemented:
			err = errors.Join(domain.ErrUnsupported, err)
		case status.Code(err) == codes.ResourceExhausted:
			err = errors.Join(domain.ErrCapacity, err)
		}
		return domain.Statistics{}, errors.Join(err, ctx.Err())
	}
	if result == nil {
		return domain.Statistics{}, domain.ErrUnavailable
	}
	return domain.Statistics{Goroutines: result.NumGoroutine, GCCycles: result.NumGC, HeapAllocatedBytes: result.Alloc, HeapTotalAllocatedBytes: result.TotalAlloc, RuntimeSysBytes: result.Sys, Allocations: result.Mallocs, Frees: result.Frees, LiveObjects: result.LiveObjects, GCPauseTotalNS: result.PauseTotalNs, APIUptimeSeconds: result.Uptime}, nil
}
