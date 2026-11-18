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

type infoReaderFunc func(context.Context) ([]domain.Processor, error)

func (read infoReaderFunc) Read(ctx context.Context) ([]domain.Processor, error) { return read(ctx) }
func sampleProcessors() []domain.Processor {
	vendor, family, model, name, step, code := "Vendor", "6", "23", "Model: processor", "0", "0x123"
	pkg, core := int32(0), int32(3)
	cores, siblings := uint32(4), uint32(8)
	mhz := 0.0
	cache := uint64(1<<64 - 1024)
	return []domain.Processor{{ID: 4, VendorID: &vendor, Family: &family, Model: &model, ModelName: &name, Stepping: &step, Microcode: &code, PackageID: &pkg, CoreID: &core, ReportedCores: &cores, ReportedSiblings: &siblings, ReportedMHz: &mhz, CacheBytes: &cache, ReportedFeatures: []string{"f1", "f2"}, ReportedBugs: []string{"b1"}}, {ID: 9, ReportedFeatures: []string{}, ReportedBugs: []string{}}}
}

type frequencyReaderFunc func(context.Context) (domain.Frequencies, error)

func (read frequencyReaderFunc) Read(ctx context.Context) (domain.Frequencies, error) {
	return read(ctx)
}

func sampleFrequencies() domain.Frequencies {
	return domain.Frequencies{InterfacePresent: true, Policies: []domain.FrequencyPolicy{{ID: 2, RelatedCPUs: []uint32{2, 3}, AffectedCPUs: []uint32{2}, ScalingCurrentKHz: new(uint64(9007199254740993)), HardwareMinKHz: new(uint64(0)), Driver: new("driver"), AvailableKHz: []uint64{9007199254740993}, Unavailable: []domain.FrequencyAttribute{{Attribute: "cpuinfo_cur_freq", Reason: "permission_denied"}}}}}
}
