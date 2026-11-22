package domain

import "time"

type ApplyMode string

const (
	Unchanged   ApplyMode = "unchanged"
	LiveRouting ApplyMode = "live_routing"
	Restart     ApplyMode = "restart"
)

type Difference struct {
	Section string
	Before  uint32
	After   uint32
}

// Assessment describes native equivalence, never effective-state attestation.
// Payload is private input for the supported live operation, not preview output.
type Assessment struct {
	Mode        ApplyMode
	Reason      string
	Differences []Difference
	Payload     []byte
}

type Plan struct {
	ID                  string
	InstanceID          string
	ExpectedRevision    uint64
	PreviousSHA256      string
	CandidateSHA256     string
	ExecutionID         string
	Generation          uint64
	Mode                ApplyMode
	Reason              string
	Differences         []Difference
	ConnectionsRetained bool
	CreatedAt           time.Time
	ExpiresAt           time.Time
}

// Application contains no configuration or native diagnostics. Persistence,
// engine effect and API readiness are independent evidence. Completed does not
// attest payload traffic, an externally modified engine, assets or endpoint identity.
type Application struct {
	ID                string
	InstanceID        string
	Epoch             string
	ExpectedRevision  uint64
	Revision          uint64
	PreviousRevision  uint64
	CandidateSHA256   string
	Mode              ApplyMode
	Outcome           string
	Phase             string
	EngineEffect      string
	Acknowledged      bool
	Readiness         string
	Error             string
	ExecutionID       string
	Generation        uint64
	Persistence       Persistence
	ReceiptDurable    bool
	RecoveryAvailable bool
	RequiresRecovery  bool
	StartedAt         time.Time
	CompletedAt       *time.Time
	Recovery          *Recovery
}

type Recovery struct {
	ID               string
	ExpectedRevision uint64
	Revision         uint64
	Outcome          string
	EngineEffect     string
	Readiness        string
	Error            string
	ExecutionID      string
	Generation       uint64
	Persistence      Persistence
	StartedAt        time.Time
	CompletedAt      *time.Time
}

// StoredApplication is private config recovery input; never encode Previous in
// a public receipt. It retains at most one original ordered source collection.
type StoredApplication struct {
	Application Application
	Previous    Desired
}

type LiveResult struct {
	Effect       string
	Acknowledged bool
}
