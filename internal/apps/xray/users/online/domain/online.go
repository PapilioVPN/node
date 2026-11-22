// Package domain describes transient, instance-scoped native online evidence.
package domain

import (
	"errors"
	"time"
)

var (
	ErrInvalidArgument  = errors.New("invalid online selection")
	ErrNotFound         = errors.New("Xray instance not found")
	ErrNotConfigured    = errors.New("Xray online API not configured")
	ErrUnsupported      = errors.New("Xray online method not implemented")
	ErrNotRunning       = errors.New("Xray engine not running")
	ErrExecutionChanged = errors.New("Xray execution changed during online read")
	ErrCapacity         = errors.New("Xray online capacity exceeded")
	ErrUnavailable      = errors.New("Xray online observation unavailable")
)

const (
	MaxIPs = 50
	Source = "xray.stats.online"
)

type Selection struct{ InstanceID, Email string }

type State string

const (
	Online  State = "online"
	Offline State = "offline"
	// Untracked means no native map. Native reads cannot distinguish an unused or
	// missing identity from disabled statsUserOnline policy. It is not offline.
	Untracked State = "untracked"
)

// Summary counts distinct tracked IPs, not connections or account membership.
// Count is absent for an unregistered map; present zero means a tracked empty map.
type Summary struct{ Count *int64 }

// IP preserves opaque native keys. LastSeenUnixSeconds is the last AddIP time,
// not a heartbeat or durable last-online history. Native removal erases it.
type IP struct {
	Address             string
	LastSeenUnixSeconds int64
}
type Visitors struct {
	Tracked bool
	IPs     []IP
}

// Observation is a fresh read of the configured endpoint under a stable local
// generation, not endpoint attestation, per-inbound attribution or cached history.
type Observation struct {
	Selection
	Generation uint64
	ObservedAt time.Time
	Duration   time.Duration
}
type Status struct {
	Observation
	Summary
}
type IPList struct {
	Observation
	Visitors
}

func (s Summary) State() State {
	if s.Count == nil {
		return Untracked
	}
	if *s.Count == 0 {
		return Offline
	}
	return Online
}
func (v Visitors) State() State {
	if !v.Tracked {
		return Untracked
	}
	if len(v.IPs) == 0 {
		return Offline
	}
	return Online
}
