package domain

import "time"

type DefaultPolicy string

const (
	PreserveDefault  DefaultPolicy = "preserve"
	MayChangeDefault DefaultPolicy = "may_change"
)

type ConnectionPolicy string

const (
	RetainConnections    ConnectionPolicy = "retain"
	TerminateConnections ConnectionPolicy = "terminate"
)

type ReplacementRequest struct {
	Tag              string
	Configuration    []byte
	DefaultPolicy    DefaultPolicy
	ConnectionPolicy ConnectionPolicy
}

// Candidate comparison covers only the native tag/sender/proxy projection.
// Source order, effective default and established resources are not observable.
type Candidate struct {
	Tag         string
	Fingerprint string
}
type ReplacementPlan struct {
	ID                          string
	InstanceID                  string
	Generation                  uint64
	OwnerRevision               uint64
	Tag                         string
	Mode                        string // unchanged, live_remove_add, configs_restart
	Reason                      string
	DefaultState                string // unobservable; never inferred from native list order
	CurrentSHA256               string
	CandidateSHA256             string
	DefaultMayChange            bool
	ExistingConnectionsRetained bool
	NativeResourcesMayRemain    bool
	NextAction                  string
	Validation                  string
	TrafficGapPossible          bool
	CreatedAt                   time.Time
	ExpiresAt                   time.Time
}
type ReplacementStep struct {
	Kind                    string
	Effect                  string // not_attempted, in_progress, not_applied, completed, unknown
	Attempted               bool
	Acknowledged            bool
	RequiresInstanceRestart bool
	Error                   string
	CompletedAt             *time.Time
}

// ReplacementResult retains no credentials. It is volatile, last-result evidence
// and the plan identity is consumed once, including partial/unknown completion.
type ReplacementResult struct {
	PlanID                   string
	InstanceID               string
	Generation               uint64
	Tag                      string
	Outcome                  string
	Error                    string
	RequiresInstanceRestart  bool
	DefaultMayChange         bool
	NativeResourcesMayRemain bool
	Steps                    []ReplacementStep
	StartedAt                time.Time
	CompletedAt              *time.Time
}
