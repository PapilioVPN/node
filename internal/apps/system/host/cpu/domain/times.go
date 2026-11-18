package domain

import "time"

// Ticks preserves Linux USER_HZ counters without floating-point conversion.
// User includes Guest, and Nice includes GuestNice: do not add those twice.
// Counters (notably IOWait) may decrease between observations.
type Ticks struct {
	CPU       string
	User      uint64
	Nice      uint64
	System    uint64
	Idle      uint64
	IOWait    uint64
	IRQ       uint64
	SoftIRQ   uint64
	Steal     uint64
	Guest     uint64
	GuestNice uint64
}

type Times struct {
	PerCPU         bool
	TicksPerSecond uint64
	Counters       []Ticks
}

type TimesObservation struct {
	Times      Times
	ObservedAt time.Time
	Duration   time.Duration
}
