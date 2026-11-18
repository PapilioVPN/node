package domain

import (
	"errors"
	"time"
)

const DefaultSampleInterval = 100 * time.Millisecond

var ErrInvalidSampleInterval = errors.New("CPU sample interval must be positive")

type UsageRequest struct {
	PerCPU   bool
	Interval time.Duration
}

type UsageState string

const (
	UsageMeasured         UsageState = "measured"
	UsageNoProgress       UsageState = "no_progress"
	UsageCounterDecreased UsageState = "counter_decreased"
	UsageCounterOverflow  UsageState = "counter_overflow"
	UsageAdded            UsageState = "added"
	UsageRemoved          UsageState = "removed"
)

// Usage reports a percentage only when the two raw samples form a valid window.
// Busy excludes idle and I/O wait; User/Nice already include guest time.
type Usage struct {
	CPU        string
	State      UsageState
	Percent    *float64
	BusyTicks  *uint64
	TotalTicks *uint64
}

type UsageObservation struct {
	PerCPU            bool
	TicksPerSecond    uint64
	CPUs              []Usage
	RequestedInterval time.Duration
	StartedAt         time.Time
	SampleInterval    time.Duration
	ObservedAt        time.Time
	Duration          time.Duration
}
