package natsapi_test

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
