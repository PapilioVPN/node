package domain

import "time"

// ReplacementInput selects an existing exact tagged inbound. Live replacement
// affects only that handler; it does not apply policies, source Env or features.
type ReplacementInput struct {
	Tag                string
	Configuration      []byte
	AllowInterruption  bool
	DisconnectExisting bool
}

// ReplacementPlan is a 30-second, process-local, single-use approval. It binds
// the exact candidate to construction settings and the locally observed execution.
// External native writers, live account changes and endpoint identity are not
// fenced by Xray. The token cannot survive a Node restart.
type ReplacementPlan struct {
	Token               string
	InstanceID          string
	Generation          uint64
	Tag                 string
	CurrentSHA256       string
	CandidateSHA256     string
	Mode                string
	Reason              string
	ExistingConnections string
	NewConnections      string
	Fallback            string
	ExpiresAt           time.Time
}

type ReplacementStep struct {
	Attempted    bool
	Effect       Effect
	Acknowledged bool
}

// ReplacementProgress remains available in control-status after a lost response.
// No automatic replay or reconstruction of live accounts from ListInbounds occurs.
type ReplacementProgress struct {
	Token      string
	Remove     ReplacementStep
	Add        ReplacementStep
	NextAction string
}
