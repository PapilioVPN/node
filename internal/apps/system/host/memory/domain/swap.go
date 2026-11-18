package domain

import "time"

// SwapSnapshot separates current capacity from cumulative I/O since kernel boot.
// Zero capacity is valid; historical I/O need not be zero after swap is disabled.
// Capacity and I/O are separate native reads, not an atomic snapshot or cgroup quota.
type SwapSnapshot struct {
	TotalBytes      uint64
	UsedBytes       uint64
	FreeBytes       uint64
	SwappedInBytes  uint64
	SwappedOutBytes uint64
	UsedPercent     float64
}
type SwapObservation struct {
	Swap       SwapSnapshot
	ObservedAt time.Time
	Duration   time.Duration
}
