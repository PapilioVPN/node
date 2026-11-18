package grpcapi_test

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
)

type readerFunc func(context.Context) (domain.Averages, error)

func (read readerFunc) Read(ctx context.Context) (domain.Averages, error) { return read(ctx) }

type timesReaderFunc func(context.Context, bool) (domain.Times, error)

func (read timesReaderFunc) Read(ctx context.Context, perCPU bool) (domain.Times, error) {
	return read(ctx, perCPU)
}
func sampleTimes(perCPU bool) domain.Times {
	return domain.Times{PerCPU: perCPU, TicksPerSecond: 100, Counters: []domain.Ticks{{CPU: "cpu9", User: 1<<64 - 1, Nice: 2, System: 3, Idle: 4, IOWait: 5, IRQ: 6, SoftIRQ: 7, Steal: 8, Guest: 9, GuestNice: 10}}}
}

func usageReader() timesReaderFunc {
	calls := 0
	return timesReaderFunc(func(_ context.Context, perCPU bool) (domain.Times, error) {
		calls++
		if !perCPU {
			return domain.Times{TicksPerSecond: 100, Counters: []domain.Ticks{{CPU: "cpu-total", User: uint64(calls) * 25, Idle: uint64(calls) * 75}}}, nil
		}
		rows := []domain.Ticks{{CPU: "cpu0"}, {CPU: "cpu1"}, {CPU: "cpu2", IOWait: 10}, {CPU: "cpu3"}, {CPU: "cpu4"}}
		if calls%2 == 0 {
			rows = []domain.Ticks{{CPU: "cpu0", User: 25, Idle: 75}, {CPU: "cpu1"}, {CPU: "cpu2", IOWait: 9}, {CPU: "cpu3", User: 1<<64 - 1, Nice: 1}, {CPU: "cpu5"}}
		}
		return domain.Times{PerCPU: perCPU, TicksPerSecond: 100, Counters: rows}, nil
	})
}
