package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	native "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/xray/domain"
)

// ReadRuntimeInfo performs one bounded GetSysStats call with no application retry.
func (c *Adapter) ReadRuntimeInfo(ctx context.Context) (domain.RuntimeInfo, error) {
	if err := ctx.Err(); err != nil {
		return domain.RuntimeInfo{}, err
	}
	ctx, done, err := c.API.Begin(ctx)
	if err != nil {
		return domain.RuntimeInfo{}, err
	}
	defer done()
	client, err := c.API.StatsClient()
	if err != nil {
		return domain.RuntimeInfo{}, err
	}
	value, err := client.GetSysStats(ctx, &native.SysStatsRequest{})
	if cause := ctx.Err(); cause != nil {
		return domain.RuntimeInfo{}, errors.Join(cause, err)
	}
	if err != nil {
		switch status.Code(err) {
		case codes.Unimplemented:
			err = errors.Join(domain.ErrRuntimeInfoUnsupported, err)
		case codes.ResourceExhausted:
			err = errors.Join(domain.ErrAPIResourceLimit, err)
		case codes.DeadlineExceeded:
			err = errors.Join(context.DeadlineExceeded, err)
		}
		return domain.RuntimeInfo{}, fmt.Errorf("read Xray runtime information: %w", err)
	}
	return domain.RuntimeInfo{
		Goroutines: value.NumGoroutine, GCCount: value.NumGC,
		AllocatedBytes: value.Alloc, TotalAllocatedBytes: value.TotalAlloc, SysBytes: value.Sys,
		Mallocs: value.Mallocs, Frees: value.Frees, LiveObjects: value.LiveObjects,
		TotalGCPauseNS: value.PauseTotalNs, UptimeSeconds: value.Uptime, ObservedAt: time.Now().UTC(),
	}, nil
}
