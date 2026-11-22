// Package domain describes native inbound account observations.
package domain

import (
	"errors"
	"time"
)

var (
	ErrStale            = errors.New("account observation expired or superseded")
	ErrUncertain        = errors.New("replace the uncertain account execution")
	ErrInvalidResponse  = errors.New("invalid native account response")
	ErrInvalidArgument  = errors.New("invalid account selection")
	ErrNotFound         = errors.New("instance not found")
	ErrUnsupported      = errors.New("account API not configured or implemented")
	ErrNotRunning       = errors.New("instance not running")
	ErrCapacity         = errors.New("account operation capacity exceeded")
	ErrExecutionChanged = errors.New("execution changed during account observation")
)

const MaxAccountBytes = 1 << 20

// Record retains deterministic native protocol.User protobuf, including credentials and
// unknown fields. It is runtime readback, not a desired-state record or a promise
// that every protocol can reconstruct its original source settings.
type Record struct {
	Email            string
	Level            uint32
	Configuration    []byte
	AccountType      string
	HasUnknownFields bool
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
