// Package domain describes exact, transient Xray byte counters.
package domain

import (
	"errors"
	"time"
)

var (
	ErrInvalidArgument  = errors.New("invalid traffic selection")
	ErrNotFound         = errors.New("Xray instance not found")
	ErrCounterNotFound  = errors.New("Xray traffic counter not found")
	ErrUnsupported      = errors.New("Xray traffic API not configured or implemented")
	ErrNotRunning       = errors.New("Xray instance is not running")
	ErrExecutionChanged = errors.New("Xray execution changed during traffic read")
	ErrClosed           = errors.New("Xray traffic resource closed")
	ErrCapacity         = errors.New("Xray traffic capacity exceeded")
	ErrUnavailable      = errors.New("Xray traffic unavailable")
)

type Scope string

const (
	User     Scope = "user"
	Inbound  Scope = "inbound"
	Outbound Scope = "outbound"
)

type Direction string

const (
	Uplink   Direction = "uplink"
	Downlink Direction = "downlink"
)

// Selection names one counter. Name is an opaque email or handler tag.
type Selection struct {
	Scope     Scope
	Name      string
	Direction Direction
}

const Source = "xray.stats.get_stats"

// Observation retains signed native bytes, including zero and wrapped values.
// Absence is ErrCounterNotFound, never a successful zero. Counters can reset
// independently of the locally observed generation; these are not billing totals.
// Generation does not attest that the configured endpoint belongs to the process.
type Observation struct {
	InstanceID string
	Generation uint64
	Selection  Selection
	Bytes      int64
	Source     string
	ObservedAt time.Time
	Duration   time.Duration
}
