// Package domain describes sanitized Xray console observations and resume positions.
package domain

import (
	"errors"
	"time"
)

var (
	ErrInvalidArgument  = errors.New("invalid log selection")
	ErrNotFound         = errors.New("log instance not found")
	ErrUnsupported      = errors.New("console output unavailable")
	ErrCapacity         = errors.New("log read capacity exhausted")
	ErrExecutionChanged = errors.New("log execution changed")
	ErrInvalidResponse  = errors.New("invalid output source response")
	ErrClosed           = errors.New("output source closed")
)

const (
	MaxRecords          = 50
	MaxReadBytes        = 64 << 10
	MaxOutputBytes      = 1 << 20
	MaxLineBytes        = 4096
	MaxWaitMilliseconds = 1000
	MaxConcurrentReads  = 32
	MaxInstanceReads    = 4
)

type TailRequest struct{ Limit uint32 }

// FollowRequest is a bounded pull lease, not a retained server subscription.
// Zero wait returns immediately; clients continue with the last received cursor.
type FollowRequest struct {
	Cursor                  string
	Limit, WaitMilliseconds uint32
}

// Chunk is private raw output, never a public log result. Offsets count raw bytes
// in the merged stdout/stderr prefix; no timestamp or stream attribution is invented.
type Chunk struct {
	ExecutionID                                            string
	Generation, Offset, NextOffset                         uint64
	Data                                                   []byte
	CapacityReached, PositionUncertain, Reattached, Exited bool
	ObservedAt                                             time.Time
}

// Record contains only a safe classification. Native text, identities, endpoints,
// credentials and diagnostic paths are deliberately absent. ObservedAt on Page is
// Node receipt time, not an engine event timestamp.
type Record struct {
	StartOffset, NextOffset      uint64
	Level, Kind, Message         string
	Redacted, Truncated, Partial bool
}

// Gap has an exact retained byte range when known. Loss after native output
// capacity or reattached exit has unknown extent and is never given a fake count.
type Gap struct {
	Reason               string
	FromOffset, ToOffset uint64
	ExtentKnown          bool
	PreviousExecutionID  string
}

// Page is volatile console evidence. CapacityReached means the source retains a
// prefix only: restart is needed to capture subsequent output with this source.
// File-configured native logs and output from older executions are not included.
// Native logging can drop records before writing console bytes; this source cannot
// detect those drops or attest complete engine event delivery.
type Page struct {
	InstanceID, Source, ExecutionID                        string
	Generation                                             uint64
	ObservedAt                                             time.Time
	Records                                                []Record
	Gaps                                                   []Gap
	NextCursor                                             string
	CapacityReached, PositionUncertain, Reattached, Exited bool
}
