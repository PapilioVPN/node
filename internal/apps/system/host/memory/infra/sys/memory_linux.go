// Package sys adapts the native Go SDK's Linux physical-memory reader.
package sys

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/PapilioVPN/node/internal/apps/system/host/memory/domain"
	"github.com/shirou/gopsutil/v4/common"
	"github.com/shirou/gopsutil/v4/mem"
)

type Reader struct{}

// Read reads Linux system-wide memory. Ordinary proc I/O is synchronous;
// cancellation is checked around the read rather than claiming it interrupts I/O.
func (Reader) Read(ctx context.Context) (domain.Snapshot, error) { return read(ctx, "/proc") }

func read(ctx context.Context, procRoot string) (domain.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return domain.Snapshot{}, err
	}
	// Bind the standard Linux source explicitly; HOST_PROC or a caller's context
	// must not silently select another host. Tests use their own private proc root.
	ctx = context.WithValue(ctx, common.EnvKey, common.EnvMap{common.HostProcEnvKey: procRoot})
	stats, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return domain.Snapshot{}, errors.Join(ctx.Err(), fmt.Errorf("read host memory: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return domain.Snapshot{}, err
	}
	if stats.Total == 0 || stats.Available > stats.Total || stats.Free > stats.Total || stats.Used > stats.Total || math.IsNaN(stats.UsedPercent) || math.IsInf(stats.UsedPercent, 0) {
		return domain.Snapshot{}, errors.New("invalid native memory observation")
	}
	return domain.Snapshot{
		TotalBytes:     stats.Total,
		AvailableBytes: stats.Available,
		UsedBytes:      stats.Used,
		FreeBytes:      stats.Free,
		ActiveBytes:    stats.Active,
		InactiveBytes:  stats.Inactive,
		BuffersBytes:   stats.Buffers,
		CachedBytes:    stats.Cached,
		SharedBytes:    stats.Shared,
		SlabBytes:      stats.Slab,
		UsedPercent:    stats.UsedPercent,
	}, nil
}
