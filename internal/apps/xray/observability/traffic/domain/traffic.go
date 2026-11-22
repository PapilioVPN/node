// Package domain describes scoped, transient Xray byte counters.
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
	ErrExecutionChanged = errors.New("Xray execution changed during traffic operation")
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

var (
	ErrContinuation    = errors.New("traffic continuation expired or changed")
	ErrConflict        = errors.New("traffic reset revision conflict")
	ErrResetUnknown    = errors.New("traffic reset effect unknown")
	ErrRestartRequired = errors.New("traffic reset requires execution replacement")
)

const (
	CollectionSource   = "xray.stats.query_stats"
	MaxCounters        = 65536
	MaxCollectionBytes = 32 << 20
	MaxPageSize        = 50
	MaxResetCounters   = 1000
)

type Counter struct {
	Selection Selection
	Bytes     int64
}

type PageRequest struct {
	Scope     Scope
	PageSize  uint32
	PageToken string
}

// Page is a bounded slice of one non-atomic native scan. Continuations perform
// no native I/O, expire after one minute and reject execution replacement.
type Page struct {
	InstanceID    string
	Generation    uint64
	Scope         Scope
	Counters      []Counter
	Source        string
	ObservedAt    time.Time
	Duration      time.Duration
	NextPageToken string
}

// Pair uses two independently sampled native counters; nil is absence.
type Pair struct {
	Source     string
	ObservedAt time.Time
	Duration   time.Duration
	InstanceID string
	Generation uint64
	Scope      Scope
	Name       string
	Uplink     *Observation
	Downlink   *Observation
}

type ResetKind string

const (
	ResetExact ResetKind = "counter"
	ResetPair  ResetKind = "pair"
	ResetScope ResetKind = "scope"
)

// ResetRequest binds destructive intent to one Node epoch and control revision.
// It must not be replayed with a freshly read revision after an ambiguous reply.
type ResetRequest struct {
	Epoch            string
	OperationID      string
	ExpectedRevision uint64
	Kind             ResetKind
	Selection        Selection
	MaxCounters      uint32
}
type Effect string

const (
	NotAttempted Effect = "not_attempted"
	NotApplied   Effect = "not_applied"
	Acknowledged Effect = "acknowledged"
	Missing      Effect = "missing"
	Unknown      Effect = "unknown"
)

type ResetItem struct {
	Selection      Selection
	Attempted      bool
	Effect         Effect
	PreviousBytes  *int64
	AcknowledgedAt *time.Time
	Error          string
}

// ResetReport is volatile last-operation evidence, readable during execution.
// Items are paged, while Total covers the entire discovered plan. A completed
// acknowledgement never proves delivery to the external management caller.
type ResetReport struct {
	InstanceID       string
	Epoch            string
	OperationID      string
	Revision         uint64
	Generation       uint64
	Kind             ResetKind
	Scope            Scope
	Name             string
	Direction        Direction
	Outcome          string
	Error            string
	RecoveryRequired bool
	StartedAt        time.Time
	CompletedAt      *time.Time
	Items            []ResetItem
	Total            uint32
	NextOffset       uint32
}
