package domain

import "time"

const (
	MaxCollectionBytes   = 4 << 20
	MaxCollectionRecords = 65536
	MaxPageSize          = 50
	MaxObservations      = 8
	MaxRetainedBytes     = 32 << 20
)

// Metadata deliberately excludes account material and native User bytes.
type Metadata struct {
	Email       string
	Level       uint32
	AccountType string
}
type MetadataAccount struct {
	Observation
	RequestedEmail string
	User           *Metadata
}

// ReadCost reports the complete uncompressed native protobuf payload and native
// RPC duration, even when only metadata is returned. This is not a heap bound.
type ReadCost struct {
	NativeBytes    int
	NativeDuration time.Duration
}
type NativeRecords struct {
	Users []Record
	ReadCost
}

// Membership is an owned, bounded native readback for in-process consumers.
type Membership struct {
	Observation
	Users []Record
}

type NativeMetadata struct {
	Users []Metadata
	ReadCost
}
type PageRequest struct {
	Size   uint32
	Cursor string
}

// PageEvidence identifies an immutable retained native observation. Membership
// writes do not refresh it. Continuations expire after 30 seconds and reject an
// execution change; active mutations can reject reads as busy. This is not an
// engine-wide atomic snapshot or a desired-configuration export.
type PageEvidence struct {
	NextCursor string
	ExpiresAt  time.Time
	ReadCost
}
type AccountPage struct {
	Observation
	Users []Record
	PageEvidence
}
type MetadataPage struct {
	Observation
	Users []Metadata
	PageEvidence
}
