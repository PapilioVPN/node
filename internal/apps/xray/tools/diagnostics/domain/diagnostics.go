// Package domain describes credential-free diagnostic evidence, not engine control.
package domain

import (
	"errors"
	"time"
)

var (
	ErrInvalidArgument = errors.New("invalid diagnostic selection")
	ErrNotFound        = errors.New("diagnostic instance not found")
	ErrCapacity        = errors.New("diagnostic capacity exceeded")
	ErrUnavailable     = errors.New("diagnostic source unavailable")
	ErrUnsupported     = errors.New("diagnostic facility unsupported")
	ErrMalformed       = errors.New("invalid diagnostic source response")
)

const (
	MaxReportBytes  = 32 << 10
	MaxVersionBytes = 8192
	MaxNativeBytes  = 64 << 10
	MaxServices     = 64
)

type Evidence struct {
	State      string
	Reason     string
	Source     string
	ObservedAt *time.Time
	Freshness  string
	Cause      error
}
type Binary struct {
	Evidence
	Version      string
	Build        string
	GoVersion    string
	OS           string
	Architecture string
	Scope        string
}
type Capability struct {
	Name  string
	State string
	Basis string
}
type Native struct {
	Generation *uint64
	Evidence
	Runtime       Evidence
	Discovery     Evidence
	UptimeSeconds *uint64
	Capabilities  []Capability
}
type Execution struct {
	Evidence
	State           string
	Generation      uint64
	AppliedRevision *uint64
	StartedAt       *time.Time
	ExitedAt        *time.Time
	ExitCode        *int32
	FailureReason   string
	CleanupReason   string
}
type Configuration struct {
	Evidence
	DesiredRevision *uint64
	UpdatedAt       *time.Time
	Durable         *bool
	MatchesApplied  *bool
}
type Request struct {
	InstanceID    string
	MaxAgeSeconds uint32
}
type Report struct {
	InstanceID    string
	ObservedAt    time.Time
	Duration      time.Duration
	MaxAgeSeconds uint32
	Consistency   string
	Execution     Execution
	Configuration Configuration
	Binary        Binary
	Native        Native
}
