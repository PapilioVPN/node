// Package domain describes logger-control outcomes, not native logger health.
package domain

import (
	"errors"
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

var (
	ErrInvalidArgument  = errors.New("invalid instance selection")
	ErrNotFound         = errors.New("Xray instance not found")
	ErrUnsupported      = errors.New("logger API not configured or implemented")
	ErrNotRunning       = errors.New("Xray instance is not running")
	ErrCapacity         = errors.New("logger control capacity exceeded")
	ErrUncertain        = errors.New("replace the uncertain execution before another logger request")
	ErrExecutionChanged = errors.New("execution changed during logger control")
)

type Effect string

const (
	NotApplied Effect = "not_applied"
	Completed  Effect = "completed"
	Unknown    Effect = "unknown"
)

// NativeResult separates a received acknowledgement from an unresolved remote
// call. Xray ignores context while opening files; timeout does not abort that I/O.
type NativeResult struct {
	Effect                  Effect
	Acknowledged            bool
	RequiresInstanceRestart bool
}

// Operation records this Node request's evidence. Even acknowledged reopening
// does not prove that native asynchronous log writers flushed or joined.
type Operation struct {
	InstanceID              string
	Generation              uint64
	Effect                  Effect
	Acknowledged            bool
	RequiresInstanceRestart bool
	ObservedAt              time.Time
	Duration                time.Duration
}

// ControlStatus reports local request state. It is not native logger-state
// readback. LastOperation is volatile, generation-scoped evidence, not a journal.
type ControlStatus struct {
	InstanceID              string
	Generation              uint64
	ExecutionState          instances.State
	APIConfigured           bool
	RequestInFlight         bool
	RequiresInstanceRestart bool
	LastOperation           *Operation
	ObservedAt              time.Time
}
