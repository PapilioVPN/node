// Package domain describes Xray runtime counters independently of native RPCs.
package domain

import (
	"errors"
	"time"
)

var (
	ErrInvalidArgument  = errors.New("invalid instance selection")
	ErrNotFound         = errors.New("Xray instance not found")
	ErrUnsupported      = errors.New("Xray statistics API not configured or implemented")
	ErrNotRunning       = errors.New("Xray instance is not running")
	ErrExecutionChanged = errors.New("Xray execution changed during statistics read")
	ErrClosed           = errors.New("Xray statistics resource closed")
	ErrCapacity         = errors.New("Xray statistics capacity exceeded")
	ErrUnavailable      = errors.New("Xray statistics unavailable")
)

// Statistics preserves all ten fields of native GetSysStats. These are engine
// Go-runtime counters, not RSS or host facts. GC/allocation counters reset with
// that runtime; APIUptimeSeconds starts at native stats-service construction.
type Statistics struct {
	Goroutines              uint32
	GCCycles                uint32
	HeapAllocatedBytes      uint64
	HeapTotalAllocatedBytes uint64
	RuntimeSysBytes         uint64
	Allocations             uint64
	Frees                   uint64
	LiveObjects             uint64
	GCPauseTotalNS          uint64
	APIUptimeSeconds        uint32
}

// Observation samples the configured API endpoint while the locally observed
// execution generation is stable. This does not attest endpoint process identity.
type Observation struct {
	InstanceID string
	Generation uint64
	Statistics Statistics
	ObservedAt time.Time
	Duration   time.Duration
}
