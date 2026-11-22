// Package domain describes outbound observations and live control independently of SDKs.
package domain

import (
	"errors"
	"time"
)

var (
	ErrUncertain        = errors.New("replace the uncertain outbound execution")
	ErrInvalidArgument  = errors.New("invalid outbound request")
	ErrNotFound         = errors.New("instance not found")
	ErrUnsupported      = errors.New("outbound API not configured or implemented")
	ErrNotRunning       = errors.New("instance is not running")
	ErrCapacity         = errors.New("outbound operation capacity exceeded")
	ErrExecutionChanged = errors.New("execution changed during outbound operation")
)

const (
	MaxConfigurationBytes = 1 << 20
	MaxResponseBytes      = 32 << 20
	MaxRecords            = 65536
)

// Record contains the native ListOutbounds projection as core.OutboundHandlerConfig
// protobuf: tag, sender settings and proxy settings. Xray does not retain source
// expire/comment fields here. This is not desired configuration or a default-route
// observation; native ordering is unspecified and untagged duplicates are retained.
type Record struct {
	Tag           string
	Configuration []byte
}
type Records struct {
	InstanceID string
	Generation uint64
	Outbounds  []Record
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
// promises durability, automatic replay or atomic replacement. For removal,
// acknowledgement does not prove termination of established connections or
// complete resource cleanup; the pinned native outbound manager does not call Close at all.
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
