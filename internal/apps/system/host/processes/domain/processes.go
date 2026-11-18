// Package domain defines observations of processes visible in Node's PID namespace.
package domain

import (
	"errors"
	"time"
)

var (
	ErrInvalidArgument   = errors.New("invalid process selection")
	ErrNotFound          = errors.New("process no longer visible")
	ErrPermissionDenied  = errors.New("process access denied")
	ErrResourceExhausted = errors.New("process inventory exceeds supported bound")
)

// Process is a single /proc/PID/stat observation. PID plus StartTicks distinguishes
// reuse within the same boot and PID namespace. CPU ticks are cumulative, not rates.
type Process struct {
	PID           int32
	ParentPID     int32
	Name          string
	State         string
	Threads       uint32
	Priority      int32
	Nice          int32
	StartTicks    uint64
	UserTicks     uint64
	SystemTicks   uint64
	VirtualBytes  uint64
	ResidentBytes uint64
}

// Selection uses an exclusive PID cursor. Each page enumerates current PIDs;
// neither a page nor traversal across pages is an atomic process snapshot.
type Selection struct {
	AfterPID int32
	Limit    uint32
}

type Entry struct {
	PID     int32
	Process *Process
	// Empty on success; otherwise not_found, permission_denied or unavailable.
	Issue string
}

type Page struct {
	Entries      []Entry
	NextAfterPID *int32
}

type Observation struct {
	Process        Process
	TicksPerSecond uint64
	ObservedAt     time.Time
	Duration       time.Duration
}

type ListObservation struct {
	Page           Page
	TicksPerSecond uint64
	ObservedAt     time.Time
	Duration       time.Duration
}

type CountObservation struct {
	Count      uint64
	ObservedAt time.Time
	Duration   time.Duration
}
