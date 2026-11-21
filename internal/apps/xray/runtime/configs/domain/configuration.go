// Package domain describes native Xray configuration building.
package domain

import (
	"errors"
	"time"
)

var (
	ErrInvalidArgument = errors.New("invalid configuration input")
	ErrNotFound        = errors.New("Xray instance not found")
	ErrUnsupported     = errors.New("configuration workspace not configured")
	ErrClosed          = errors.New("configuration builder closed")
	ErrCapacity        = errors.New("configuration building capacity exceeded")
	ErrTooLarge        = errors.New("candidate exceeds supported byte limit")
)

// MaxCandidateBytes bounds one supported candidate, independently of possibly
// smaller incoming transport limits. It is not a bound on the native builder heap.
const MaxCandidateBytes = 1 << 20

type Format string

const (
	JSON     Format = "json"
	YAML     Format = "yaml"
	TOML     Format = "toml"
	Protobuf Format = "protobuf"
)

type Candidate struct {
	Format  Format
	Content []byte
}

// Validation reports native building, never runtime construction or readiness.
// Failure retains a rejected converter's private cause, not public diagnostics.
type Validation struct {
	InstanceID string
	Valid      bool
	CheckedAt  time.Time
	Duration   time.Duration
	Failure    error
}
