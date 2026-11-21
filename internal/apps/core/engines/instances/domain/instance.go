// Package domain describes engine executions independently of OS and transports.
package domain

import (
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
	State      State
	Generation uint64
	StartedAt  *time.Time
	ExitedAt   *time.Time
	ExitCode   *int32
	Forced     bool
	Failure    error
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

type Readiness struct {
	Status        Status
	APIConfigured bool
	APIReady      bool
	CheckedAt     time.Time
}
