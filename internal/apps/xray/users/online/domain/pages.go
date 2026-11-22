package domain

import (
	"errors"
	"time"
)

const (
	MaxPageSize         = 50
	MaxRecords          = 65536
	MaxNativeBytes      = 4 << 20
	MaxObservationBytes = 4 << 20
	MaxObservations     = 8
	ObservationTTL      = 30 * time.Second
)

var (
	ErrObservationExpired  = errors.New("online observation expired or unknown")
	ErrIdentityUnsupported = errors.New("native bulk online identity projection is lossy")
)

type PageRequest struct {
	Selection
	PageSize  int
	PageToken string
}

// Record is either an online email (IP absent), or one native visitor of that
// email. Detailed pages count visitor records, so a large IP set is also paged.
// Collections contain only observed online entries, never a membership inventory.
type Record struct {
	Email string
	IP    *IP
}

// Page continues a bounded retained native observation. It is not an atomic
// engine snapshot. ObservedAt never advances while paging; ServedAt does.
// Continuations expire absolutely and reject local execution changes. Node
// restart loses all continuations. No native read is repeated for continuation.
type Page struct {
	Observation
	Records       []Record
	State         State
	Source        string
	ObservationID string
	ExpiresAt     time.Time
	ServedAt      time.Time
	Fresh         bool
	Total         int
	NextPageToken string
}
