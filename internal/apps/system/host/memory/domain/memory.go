// Package domain defines physical RAM and swap observations in exact bytes.
package domain

import "time"

// Snapshot reports system-wide physical memory, not a container's cgroup limit.
// UsedBytes is TotalBytes minus AvailableBytes. CachedBytes includes reclaimable
// slab, following the selected native SDK. Counters are not an atomic OS snapshot.
type Snapshot struct {
	TotalBytes     uint64
	AvailableBytes uint64
	UsedBytes      uint64
	FreeBytes      uint64
	ActiveBytes    uint64
	InactiveBytes  uint64
	BuffersBytes   uint64
	CachedBytes    uint64
	SharedBytes    uint64
	SlabBytes      uint64
	UsedPercent    float64
}

type Observation struct {
	Memory     Snapshot
	ObservedAt time.Time
	Duration   time.Duration
}
