// Package domain describes live Xray inbound records and control evidence.
package domain

import (
	"errors"
	"time"
)

var (
	ErrInvalidArgument  = errors.New("invalid inbound input")
	ErrNotFound         = errors.New("instance not found")
	ErrUnsupported      = errors.New("inbound API not configured or implemented")
	ErrNotRunning       = errors.New("instance is not running")
	ErrCapacity         = errors.New("inbound operation capacity exceeded")
	ErrExecutionChanged = errors.New("execution changed during inbound operation")
	ErrUncertain        = errors.New("replace the uncertain inbound execution")
)

const (
	MaxConfigurationBytes = 1 << 20
	MaxResponseBytes      = 32 << 20
	MaxRecords            = 65536
)

// Record retains native core.InboundHandlerConfig protobuf, including nested
// TypedMessage settings and unknown fields. Tag is an observation projection.
type Record struct {
	Tag           string
	Configuration []byte
}
type Records struct {
	InstanceID string
	Generation uint64
	Inbounds   []Record
	ObservedAt time.Time
	Duration   time.Duration
}

// Tags is a separate native projection; duplicates/empty untagged values and the
// native unspecified ordering are retained, without fetching configurations.
type Tags struct {
	InstanceID string
	Generation uint64
	Tags       []string
	ObservedAt time.Time
	Duration   time.Duration
}
type Effect string

const (
	NotApplied Effect = "not_applied"
	Completed  Effect = "completed"
	Unknown    Effect = "unknown"
)

type NativeResult struct {
	Tag                     string
	Effect                  Effect
	Acknowledged            bool
	RequiresInstanceRestart bool
}

// Operation reports live effects. It neither rewrites desired configuration nor
// promises durability, automatic replay or atomic replacement.
type Operation struct {
	InstanceID              string
	Generation              uint64
	Kind                    string
	Tag                     string
	Effect                  Effect
	Acknowledged            bool
	RequiresInstanceRestart bool
	ObservedAt              time.Time
	Duration                time.Duration
}
type ControlStatus struct {
	InstanceID              string
	Generation              uint64
	ExecutionState          string
	APIConfigured           bool
	RequestInFlight         bool
	RequiresInstanceRestart bool
	LastOperation           *Operation
	ObservedAt              time.Time
}
