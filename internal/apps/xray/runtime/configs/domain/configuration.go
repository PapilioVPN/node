// Package domain describes native Xray sources, preparation and desired state.
package domain

import (
	"errors"
	"time"
)

var (
	ErrInvalidArgument   = errors.New("invalid configuration input")
	ErrNotFound          = errors.New("Xray instance not found")
	ErrUnsupported       = errors.New("configuration resource not configured")
	ErrClosed            = errors.New("configuration resources closed")
	ErrCapacity          = errors.New("configuration capacity exceeded")
	ErrTooLarge          = errors.New("candidate exceeds supported byte limit")
	ErrRejected          = errors.New("native configuration builder rejected input")
	ErrConflict          = errors.New("desired configuration revision changed")
	ErrDurabilityUnknown = errors.New("configuration publication durability is unknown")
	ErrNoDesired         = errors.New("no desired configuration exists")
	ErrNoApplication     = errors.New("configuration application not retained")
	ErrPlanExpired       = errors.New("configuration plan expired or superseded")
	ErrStalePlan         = errors.New("configuration plan is stale")
	ErrReadiness         = errors.New("configuration execution readiness not established")
)

// MaxCandidateBytes bounds one supported candidate, independently of possibly
// smaller incoming transport limits. It is not a bound on the native builder heap.
const MaxCandidateBytes = 1 << 20

const (
	MaxSources       = 32
	MaxCompiledBytes = 32 << 20
)

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

// Source names are native basenames, including their format extension. Ordering
// and names (notably Xray's tail convention) participate in native merging.
type Source struct {
	Name    string
	Content []byte
}

// Compilation owns executable protobuf bytes, which may contain credentials.
// Source-level environment settings are not represented in native protobuf.
// SHA256 identifies Content, not the original sources or a desired-state revision.
// A successful build is not proof of runtime acceptance or applied state.
type Compilation struct {
	InstanceID string
	Content    []byte
	SHA256     string
	PreparedAt time.Time
	Duration   time.Duration
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
