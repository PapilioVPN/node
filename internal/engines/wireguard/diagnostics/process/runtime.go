package process

import (
	"context"
	"runtime"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
)

type Runtime struct {
	scope func(context.Context, string) (time.Time, error)
}

type RuntimeStats struct {
	Goroutines         int
	GCCount            uint32
	HeapBytes          uint64
	AllocatedBytes     uint64
	SystemBytes        uint64
	Allocations        uint64
	Frees              uint64
	LiveObjects        uint64
	GCPauseNanoseconds uint64
	StartedAt          time.Time
}

func (service *Runtime) Bind(scope func(context.Context, string) (time.Time, error)) {
	service.scope = scope
}

func (service *Runtime) Read(ctx context.Context, name string) (RuntimeStats, error) {
	if service.scope == nil {
		return RuntimeStats{}, status.ErrUnavailable
	}
	startedAt, err := service.scope(ctx, name)
	if err != nil {
		return RuntimeStats{}, err
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	if err := ctx.Err(); err != nil {
		return RuntimeStats{}, err
	}
	return RuntimeStats{Goroutines: runtime.NumGoroutine(), GCCount: memory.NumGC, HeapBytes: memory.Alloc,
		AllocatedBytes: memory.TotalAlloc, SystemBytes: memory.Sys, Allocations: memory.Mallocs, Frees: memory.Frees,
		LiveObjects: memory.Mallocs - memory.Frees, GCPauseNanoseconds: memory.PauseTotalNs, StartedAt: startedAt}, nil
}

func (stats RuntimeStats) Uptime() *uint64 {
	if stats.StartedAt.IsZero() {
		return nil
	}
	seconds := uint64(max(time.Since(stats.StartedAt), 0) / time.Second)
	return &seconds
}
