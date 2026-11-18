// Package domain defines logind sessions and legacy login-file records.
package domain

import (
	"errors"
	"time"
)

var (
	ErrInvalidArgument   = errors.New("invalid session selection")
	ErrNotFound          = errors.New("session not found")
	ErrPermissionDenied  = errors.New("session source access denied")
	ErrUnavailable       = errors.New("session source unavailable")
	ErrUnsupported       = errors.New("session source unsupported on this platform")
	ErrResourceExhausted = errors.New("session inventory exceeds supported bound")
)

type Selection struct {
	Offset uint32
	Limit  uint32
}

// Summary contains only the native bulk projection. Nil means the native method
// does not report that field; known zero and known empty remain present.
type Summary struct {
	ID                   string
	UID                  uint32
	User                 string
	Seat                 string
	LeaderPID            *uint32
	Class                *string
	Terminal             *string
	Idle                 *bool
	IdleSinceMonotonicUS *uint64
}
type Page struct {
	Sessions   []Summary
	Extended   bool
	NextOffset *uint32
}
type Details struct {
	Summary            Summary
	Remote             *bool
	RemoteHost         *string
	RemoteUser         *string
	StartedUnixUS      *uint64
	StartedMonotonicUS *uint64
	Type               *string
	State              *string
	Active             *bool
}

// LoginRecord preserves legacy utmp's record identity and ordering, including
// duplicates. RecordID is not a logind session ID. No UID lookup is performed.
type LoginRecord struct {
	PID           int32
	RecordID      string
	User          string
	Terminal      string
	Host          string
	StartedUnixUS int64
	SessionID     int32
}
type RecordPage struct {
	Records    []LoginRecord
	NextOffset *uint32
}
type ListObservation struct {
	Page       Page
	ObservedAt time.Time
	Duration   time.Duration
}
type ReadObservation struct {
	Details    Details
	ObservedAt time.Time
	Duration   time.Duration
}
type RecordsObservation struct {
	Page       RecordPage
	ObservedAt time.Time
	Duration   time.Duration
}
