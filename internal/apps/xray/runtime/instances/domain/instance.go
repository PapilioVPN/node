// Package domain describes Xray executions independently of OS and transports.
package domain

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidArgument  = errors.New("invalid instance selection")
	ErrNotFound         = errors.New("instance not found")
	ErrBusy             = errors.New("instance mutation already active")
	ErrClosed           = errors.New("instance is closed")
	ErrUnsupported      = errors.New("instance facility not configured")
	ErrNotApplied       = errors.New("operation was not applied")
	ErrExecutionChanged = errors.New("execution changed during observation")
	ErrCleanupFailed    = errors.New("configuration resource cleanup failed")
)

type State string

const (
	Stopped  State = "stopped"
	Running  State = "running"
	Stopping State = "stopping"
	Exited   State = "exited"
)

// Execution retains the last run after exit. Failure is private native evidence.
// StartedAt acknowledges process creation, not engine readiness or signal admission.
type Execution struct {
	ExecutionID           string
	IdentityVerified      bool
	Reattached            bool
	PreserveOnClose       bool
	State                 State
	Generation            uint64
	StartedAt             *time.Time
	ExitedAt              *time.Time
	ExitCode              *int32
	Forced                bool
	Failure               error
	ConfigurationRevision *uint64
	CleanupFailure        error
}

type Status struct {
	ID         string
	Engine     string
	Execution  Execution
	ObservedAt time.Time
}

type Effect string

const (
	Completed  Effect = "completed"
	NotApplied Effect = "not_applied"
	Unknown    Effect = "unknown"
	Partial    Effect = "partial"
)

// Control retains observations even when an operation or its delivery fails.
// Completed does not guarantee that a reply reached the controller.
type Control struct {
	Status *Status
	Effect Effect
}

type ProbeState string

const (
	ProbeNotConfigured ProbeState = "not_configured"
	ProbeNotChecked    ProbeState = "not_checked"
	ProbeReady         ProbeState = "ready"
	ProbeUnavailable   ProbeState = "unavailable"
)

type Readiness struct {
	Status       Status
	APIState     ProbeState
	TrafficState ProbeState
	CheckedAt    time.Time
}

var (
	ErrOwnership        = errors.New("execution ownership cannot be verified")
	ErrRecoveryRequired = errors.New("execution recovery requires explicit resolution")
	ErrReadiness        = errors.New("execution failed readiness")
	ErrConflict         = errors.New("execution or lease revision conflict")
)

const MaxOutputBytes = 1 << 20
const MaxOutputRead = 64 << 10

// OutputRequest selects a bounded raw console prefix. This internal source is
// private input for Logs, whose own boundary must redact and frame records.
type OutputRequest struct {
	ExecutionID string
	Offset      uint64
	Limit       uint32
}
type OutputPage struct {
	ExecutionID       string
	Generation        uint64
	Offset            uint64
	NextOffset        uint64
	Data              []byte
	CapacityReached   bool
	PositionUncertain bool
	Reattached        bool
	Exited            bool
	ObservedAt        time.Time
}
type OutputSource interface {
	ReadOutput(context.Context, OutputRequest) (OutputPage, error)
}
type OutputBinding struct {
	ID     string
	Source OutputSource
}

type RetentionRequest struct {
	ExecutionID string
	Preserve    bool
}
type RepairRequest struct {
	ExecutionID    string
	MaxAttempts    uint32
	ReplaceUnready bool
}
type RepairResult struct {
	Control  Control
	Attempts uint32
	Ready    bool
	Action   string
}
type InstancePage struct {
	Items     []Status
	NextAfter string
}

type LeaseStatus struct {
	Revision    uint64
	State       string
	Owner       string
	ExecutionID string
	Policy      string
	ExpiresAt   *time.Time
	StopEffect  Effect
	Error       string
	ObservedAt  time.Time
}

// LeaseRecord is private storage. Token is returned only by explicit acquisition.
type LeaseRecord struct {
	Status LeaseStatus
	Token  string
}
type LeaseReceipt struct {
	Status LeaseStatus
	Token  string
}
type AcquireLease struct {
	ExpectedRevision uint64
	ExecutionID      string
	Owner            string
	TTLSeconds       uint32
	Policy           string
}
type RenewLease struct {
	ExpectedRevision uint64
	Token            string
	TTLSeconds       uint32
}
type ReleaseLease struct {
	ExpectedRevision uint64
	Token            string
}
