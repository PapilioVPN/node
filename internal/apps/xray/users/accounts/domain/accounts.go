// Package domain describes native inbound account observations.
package domain

import (
	"errors"
	"time"
)

var (
	ErrInvalidResponse  = errors.New("invalid native account response")
	ErrInvalidArgument  = errors.New("invalid account selection")
	ErrNotFound         = errors.New("instance not found")
	ErrUnsupported      = errors.New("account API not configured or implemented")
	ErrNotRunning       = errors.New("instance not running")
	ErrCapacity         = errors.New("account read capacity exceeded")
	ErrExecutionChanged = errors.New("execution changed during account observation")
)

const MaxAccountBytes = 1 << 20

// Record retains native protocol.User protobuf, including account credentials and
// unknown fields. It is runtime readback, not a desired-state record or a promise
// that every protocol can reconstruct its original source settings.
type Record struct {
	Email         string
	Level         uint32
	Configuration []byte
}
type Observation struct {
	InstanceID string
	InboundTag string
	Generation uint64
	ObservedAt time.Time
	Duration   time.Duration
}
type Count struct {
	Observation
	Count int64
}
type Account struct {
	Observation
	RequestedEmail string
	User           *Record
}
