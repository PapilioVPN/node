// Package policies calculates processor observations without native I/O.
package policies

import (
	"errors"
	"math/bits"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
)

// Usage matches logical processor identity, preserving the final sample's order
// followed by removed CPUs in initial order. Readers already validate unique IDs.
func Usage(before, after domain.Times) ([]domain.Usage, error) {
	if before.PerCPU != after.PerCPU || before.TicksPerSecond != after.TicksPerSecond {
		return nil, errors.New("CPU counter scope or units changed during sample")
	}
	remaining := make(map[string]domain.Ticks, len(before.Counters))
	for _, row := range before.Counters {
		remaining[row.CPU] = row
	}
	result := make([]domain.Usage, 0, len(after.Counters))
	for _, row := range after.Counters {
		previous, found := remaining[row.CPU]
		if !found {
			result = append(result, domain.Usage{CPU: row.CPU, State: domain.UsageAdded})
			continue
		}
		delete(remaining, row.CPU)
		result = append(result, usage(previous, row))
	}
	for _, row := range before.Counters {
		if _, removed := remaining[row.CPU]; removed {
			result = append(result, domain.Usage{CPU: row.CPU, State: domain.UsageRemoved})
		}
	}
	return result, nil
}

func usage(before, after domain.Ticks) domain.Usage {
	result := domain.Usage{CPU: after.CPU}
	// Linux includes guest counters in User/Nice. Sum only these eight deltas,
	// before converting to float: large cumulative counters retain small changes.
	first := [8]uint64{before.User, before.Nice, before.System, before.Idle, before.IOWait, before.IRQ, before.SoftIRQ, before.Steal}
	last := [8]uint64{after.User, after.Nice, after.System, after.Idle, after.IOWait, after.IRQ, after.SoftIRQ, after.Steal}
	var total uint64
	for i, value := range last {
		if value < first[i] {
			result.State = domain.UsageCounterDecreased
			return result
		}
		sum, carry := bits.Add64(total, value-first[i], 0)
		if carry != 0 {
			result.State = domain.UsageCounterOverflow
			return result
		}
		total = sum
	}
	if total == 0 {
		result.State = domain.UsageNoProgress
		return result
	}
	busy := total - (after.Idle - before.Idle) - (after.IOWait - before.IOWait)
	percent := float64(busy) / float64(total) * 100
	result.State = domain.UsageMeasured
	result.Percent = &percent
	result.BusyTicks, result.TotalTicks = &busy, &total
	return result
}
