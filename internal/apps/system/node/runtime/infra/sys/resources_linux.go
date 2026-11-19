package sys

import (
	"context"
	"errors"
	"math"
	"os"
	"runtime/metrics"

	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/domain"
	"github.com/prometheus/procfs"
	"github.com/tklauser/go-sysconf"
)

func (reader Reader) Resources(ctx context.Context) (domain.Resources, error) {
	if err := ctx.Err(); err != nil {
		return domain.Resources{}, err
	}
	fs, err := procfs.NewFS("/proc")
	if err != nil {
		return domain.Resources{}, err
	}
	process, err := fs.Self()
	if err != nil {
		return domain.Resources{}, err
	}
	stat, err := process.Stat()
	if err != nil {
		return domain.Resources{}, err
	}
	ticks, err := sysconf.Sysconf(sysconf.SC_CLK_TCK)
	if err != nil {
		return domain.Resources{}, err
	}
	result, err := processResources(stat, ticks, int64(os.Getpagesize()))
	if err != nil {
		return domain.Resources{}, err
	}
	// procfs uses the native fd-directory stat count on Linux>=6.2. On older
	// kernels its documented fallback enumerates descriptors; no per-fd metadata.
	descriptors, err := process.FileDescriptorsLen()
	if err != nil {
		return domain.Resources{}, err
	}
	if descriptors < 0 || descriptors > 1048576 {
		return domain.Resources{}, errors.New("descriptor count exceeds supported capacity")
	}
	result.FileDescriptors = uint64(descriptors)
	samples := []metrics.Sample{
		{Name: "/sched/goroutines:goroutines"}, {Name: "/sched/gomaxprocs:threads"},
		{Name: "/memory/classes/total:bytes"}, {Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/memory/classes/heap/released:bytes"}, {Name: "/gc/heap/allocs:bytes"},
		{Name: "/gc/heap/frees:bytes"}, {Name: "/gc/cycles/total:gc-cycles"},
	}
	// Samples are request-owned: concurrent reads never share SDK backing storage.
	// This reads selected gauges/counters without ReadMemStats' global STW path.
	metrics.Read(samples)
	targets := []*uint64{&result.Goroutines, &result.GoMaxProcs, &result.RuntimeMappedBytes, &result.HeapObjectsBytes, &result.HeapReleasedBytes, &result.AllocatedBytes, &result.FreedBytes, &result.GCCycles}
	for i, sample := range samples {
		if sample.Value.Kind() != metrics.KindUint64 {
			return domain.Resources{}, errors.New("required Go runtime metric unavailable")
		}
		*targets[i] = sample.Value.Uint64()
	}
	if err = ctx.Err(); err != nil {
		return domain.Resources{}, err
	}
	return result, nil
}
func processResources(stat procfs.ProcStat, ticks, pages int64) (domain.Resources, error) {
	if stat.PID < 1 || int64(stat.PID) > math.MaxInt32 || stat.RSS < 0 || stat.NumThreads < 0 || ticks <= 0 || pages <= 0 || uint64(stat.RSS) > math.MaxUint64/uint64(pages) {
		return domain.Resources{}, errors.New("invalid process resource observation")
	}
	return domain.Resources{PID: int32(stat.PID), StartTicks: stat.Starttime, TicksPerSecond: uint64(ticks), UserTicks: uint64(stat.UTime), SystemTicks: uint64(stat.STime), VirtualBytes: uint64(stat.VSize), ResidentBytes: uint64(stat.RSS) * uint64(pages), Threads: uint64(stat.NumThreads)}, nil
}
