// Package domain defines block-device and partition I/O observations.
package domain

import (
	"errors"
	"time"
)

var (
	ErrInvalidArgument   = errors.New("invalid device selection")
	ErrResourceExhausted = errors.New("device inventory exceeds supported bound")
)

// Device contains kernel-wide counters, not cgroup accounting. Sectors always
// mean 512 bytes in diskstats, independently of hardware logical sector size.
// InProgress is an instantaneous gauge; the other values are cumulative counters
// that can reset when a device is removed or its statistics are reset.
type Device struct {
	Major                uint32
	Minor                uint32
	Name                 string
	ReadsCompleted       uint64
	ReadsMerged          uint64
	ReadSectors          uint64
	ReadMilliseconds     uint64
	WritesCompleted      uint64
	WritesMerged         uint64
	WriteSectors         uint64
	WriteMilliseconds    uint64
	InProgress           uint64
	ActiveMilliseconds   uint64
	WeightedMilliseconds uint64
	Discard              *Discard
	Flush                *Flush
}
type Discard struct {
	Completed    uint64
	Merged       uint64
	Sectors      uint64
	Milliseconds uint64
}
type Flush struct {
	Completed    uint64
	Milliseconds uint64
}
type Selection struct {
	AfterName string
	Limit     uint32
}
type Page struct {
	Devices       []Device
	NextAfterName *string
}
type Observation struct {
	Page       Page
	ObservedAt time.Time
	Duration   time.Duration
}
