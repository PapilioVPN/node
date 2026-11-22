package domain

import "time"

// Summary is structural native configuration evidence, never an account list or
// credential validation. ListInbounds retains construction settings, so mutable
// live membership and runtime account fields cannot be inferred from it.
type Summary struct {
	Tag           string
	Direction     string
	Protocol      string
	NativeType    string
	AccountType   string
	LiveAccounts  bool
	Reason        string
	EmailMatching string
}

type PageRequest struct {
	Size   uint32
	Cursor string
}

// PageEvidence identifies a retained observation. Continuations do no native I/O.
// They expire after 30 seconds and reject local mutations/execution changes.
// External native writers are not fenced; observed_at stays the initial time.
type PageEvidence struct {
	NextCursor string
	ExpiresAt  time.Time
}

type Summaries struct {
	InstanceID string
	Generation uint64
	Inbounds   []Summary
	ObservedAt time.Time
	Duration   time.Duration
	PageEvidence
}

type Compatibility struct {
	InstanceID string
	Generation uint64
	Inbound    Summary
	ObservedAt time.Time
	Duration   time.Duration
}
