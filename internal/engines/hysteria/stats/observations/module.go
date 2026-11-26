// Package stats owns scoped Hysteria runtime, traffic, online and kick operations.
package observations

import (
	"context"
	"errors"
	"time"
)

const (
	MaxKickIDs       = 128
	MaxUserIDBytes   = 256
	MaxKickBodyBytes = 256 << 10
	ResetScope       = "instance"
)

var (
	ErrNotFound      = errors.New("Hysteria traffic identifier not found")
	ErrInvalid       = errors.New("invalid Hysteria statistics request")
	ErrInactive      = errors.New("Hysteria execution ended")
	ErrCapacity      = errors.New("Hysteria statistics concurrency limit reached")
	ErrTooLarge      = errors.New("Hysteria statistics snapshot is too large")
	ErrUnavailable   = errors.New("Hysteria Traffic Stats API unavailable")
	ErrEffectUnknown = errors.New("Hysteria mutation effect is unknown")
)

type Counter struct{ TX, RX uint64 }
type Entry struct {
	UserID string
	Counter
}
type OnlineUser struct {
	UserID  string
	Devices uint64
}
type Runtime struct {
	Execution          uint64
	ReadySinceUnixNano int64
	UptimeSeconds      uint64
}

type Native interface {
	Traffic(context.Context, bool) (map[string]Counter, error)
	Online(context.Context) (map[string]uint64, error)
	Kick(context.Context, []string) error
}

type Execution struct {
	Number   uint64
	ReadyAt  time.Time
	Lifetime context.Context
	Native   Native
}
