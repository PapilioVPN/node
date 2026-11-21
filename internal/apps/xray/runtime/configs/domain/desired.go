package domain

import "time"

type DesiredMetadata struct {
	Revision    uint64
	SHA256      string
	UpdatedAt   time.Time
	SourceCount int
	Durable     bool
}

// Desired retains original source bytes, including native environment settings.
// Revision zero means no Node-owned desired configuration exists.
type Desired struct {
	Metadata DesiredMetadata
	Sources  []Source
}

// Persistence separates visible publication from confirmed filesystem syncing.
// A publication followed by a sync failure must never become not-applied.
type Persistence struct {
	Metadata           DesiredMetadata
	Published          bool
	PublicationUnknown bool
	Durable            bool
}
