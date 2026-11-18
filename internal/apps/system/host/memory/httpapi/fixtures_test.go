package httpapi_test

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/system/host/memory/domain"
)

type readerFunc func(context.Context) (domain.Snapshot, error)

func (read readerFunc) Read(ctx context.Context) (domain.Snapshot, error) { return read(ctx) }

func sampleMemory() domain.Snapshot {
	return domain.Snapshot{TotalBytes: 1<<64 - 1, AvailableBytes: 3, UsedBytes: 1<<64 - 4, FreeBytes: 1, ActiveBytes: 2, InactiveBytes: 4, BuffersBytes: 8, CachedBytes: 16, SharedBytes: 32, SlabBytes: 64, UsedPercent: 99.5}
}
