package domain

import (
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
)

const ResetTrafficCounters = "xray.traffic.counter.reset_collection"

type TrafficResetState string

const (
	TrafficResetNotAttempted TrafficResetState = "not_attempted"
	TrafficResetAcknowledged TrafficResetState = "acknowledged"
	TrafficResetNotApplied   TrafficResetState = "not_applied"
	TrafficResetUnknown      TrafficResetState = "unknown"
	TrafficResetMissing      TrafficResetState = "missing"
)

// TrafficCounterResetEntry retains a selected counter even when it was not run.
// Reset is present only for an acknowledged atomic swap; zero bytes are valid.
type TrafficCounterResetEntry struct {
	Name      string
	Direction TrafficDirection
	State     TrafficResetState
	Reset     *TrafficCounterReset
}

// TrafficCountersReset describes independent effects, never an atomic scope reset.
// SelectedAt is discovery completion; newly created counters are not included.
// A name removed/recreated after discovery has no native generation identifier.
type TrafficCountersReset struct {
	Items      []TrafficCounterResetEntry
	SelectedAt time.Time
}

// InstanceTrafficCountersReset retains progress on both success and failure.
// Losing delivery of this report leaves the caller unable to infer its effects.
type InstanceTrafficCountersReset struct {
	InstanceID instances.InstanceID
	Scope      TrafficScope
	Reset      TrafficCountersReset
}
