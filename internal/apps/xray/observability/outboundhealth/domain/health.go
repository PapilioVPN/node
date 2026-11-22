// Package domain describes Xray's existing outbound observations, never probes.
package domain

import (
	"errors"
	"time"
)

var (
	ErrInvalidArgument  = errors.New("invalid outbound health selection")
	ErrNotFound         = errors.New("Xray instance not found")
	ErrDisabled         = errors.New("Xray API not configured")
	ErrUnsupported      = errors.New("native observatory service not implemented")
	ErrNotRunning       = errors.New("Xray instance not running")
	ErrExecutionChanged = errors.New("Xray execution changed during observation")
	ErrContinuation     = errors.New("outbound health continuation expired or changed")
	ErrClosed           = errors.New("native Xray API closed")
	ErrCapacity         = errors.New("outbound health capacity exceeded")
	ErrUnavailable      = errors.New("outbound health unavailable")
)

const (
	Source               = "xray.observatory.get_outbound_status"
	Ordinary             = "ordinary"
	Burst                = "burst"
	MaxNativeBytes       = 1 << 20
	MaxRecords           = 4096
	MaxTagBytes          = 4096
	MaxPageSize          = 50
	DefaultMaxAgeSeconds = 60
	MaxAgeSeconds        = 86400
)

// HealthPing retains signed native counts and nanosecond statistics. It supplies
// no sample timestamp in the pinned engine, including when All is positive.
type HealthPing struct{ All, Fail, DeviationNS, AverageNS, MaxNS, MinNS int64 }

// Record is native evidence. Failed ordinary delay is a sentinel, not latency.
// LastSeenUnixSec is the last successful probe; LastTryUnixSec is the last attempt.
// Burst reports zero for both; no timestamp may be inferred from Node receipt.
type Record struct {
	Tag             string
	Alive           bool
	DelayMS         int64
	LastSeenUnixSec int64
	LastTryUnixSec  int64
	HealthPing      *HealthPing
}

type Status struct {
	Record
	Mode             string
	LatencyMS        *int64
	SampledAt        *time.Time
	SampleAgeSeconds *int64
	Freshness        string
}

type Selection struct {
	Tag                 string
	MaxSampleAgeSeconds uint32
}
type PageRequest struct {
	PageSize            uint32
	PageToken           string
	MaxSampleAgeSeconds uint32
}
type Observation struct {
	InstanceID          string
	Generation          uint64
	Source              string
	ObservedAt          time.Time
	Duration            time.Duration
	MaxSampleAgeSeconds uint32
	Status              *Status
}
type Page struct {
	ServedAt            time.Time
	ExpiresAt           *time.Time
	Total               uint32
	InstanceID          string
	Generation          uint64
	Source              string
	ObservedAt          time.Time
	Duration            time.Duration
	MaxSampleAgeSeconds uint32
	Statuses            []Status
	NextPageToken       string
}
