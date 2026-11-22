package domain

import "time"

type Effect string

const (
	NotApplied Effect = "not_applied"
	Completed  Effect = "completed"
	Unknown    Effect = "unknown"
	// Partial is used only for a batch with a known mixture of completed and unsent work.
	Partial Effect = "partial"
)

type NativeResult struct {
	Guidance                *Guidance
	Email                   string
	Effect                  Effect
	Acknowledged            bool
	RequiresInstanceRestart bool
}

// Operation records live native effects, not durable acceptance or atomic
// replacement. Removal does not promise termination of established connections;
// each native protocol owns its actual membership/session semantics.
type Operation struct {
	Guidance                *Guidance
	InstanceID              string
	InboundTag              string
	Generation              uint64
	Kind                    string
	Email                   string
	Effect                  Effect
	Acknowledged            bool
	RequiresInstanceRestart bool
	ObservedAt              time.Time
	Duration                time.Duration
}

// ControlStatus is volatile, local evidence. It is not native membership readback
// or a durable journal. Only replacement clears an unresolved execution fence.
type ControlStatus struct {
	InstanceID              string
	Generation              uint64
	ExecutionState          string
	APIConfigured           bool
	RequestInFlight         bool
	RequiresInstanceRestart bool
	LastOperation           *Operation
	ObservedAt              time.Time
}
