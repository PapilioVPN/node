// Package domain describes private, instance-owned executable/geodata releases.
package domain

import (
	"errors"
	"time"
)

var (
	ErrInvalidArgument = errors.New("invalid asset input")
	ErrCapacity        = errors.New("asset capacity exceeded")
	ErrBusy            = errors.New("assets operation active")
	ErrConflict        = errors.New("asset revision or execution conflict")
	ErrNotFound        = errors.New("asset release not found")
	ErrUnsupported     = errors.New("asset activation not configured")
	ErrRejected        = errors.New("asset validation rejected")
	ErrRecovery        = errors.New("asset recovery required")
	ErrUnavailable     = errors.New("asset operation failed")
)

const (
	MaxBinary   = 96 << 20
	MaxGeodata  = 64 << 20
	MaxRequest  = 300 << 20 // Base64 plus envelope; transport limits may be lower.
	MaxReleases = 32
)

type File struct {
	Content []byte `json:"content"`
	SHA256  string `json:"sha256"`
}

// Candidate contains trusted executable material. Expected digests protect
// transfer integrity, not publisher authenticity. No URL or shell is accepted.
type Candidate struct {
	Binary  File `json:"binary"`
	GeoIP   File `json:"geoip"`
	GeoSite File `json:"geosite"`
}
type Artifact struct {
	SHA256  string `json:"sha256"`
	Bytes   uint64 `json:"bytes,string"`
	Entries uint32 `json:"entries"`
}
type Release struct {
	ID         string    `json:"id"`
	Version    string    `json:"version"`
	Binary     Artifact  `json:"binary"`
	GeoIP      Artifact  `json:"geoip"`
	GeoSite    Artifact  `json:"geosite"`
	PreparedAt time.Time `json:"prepared_at"`
}

// Pending is a durable rollback intent. Active remains the recovery authority
// until a ready replacement has been committed. Every attempt consumes a revision.
type State struct {
	InstanceID string `json:"instance_id"`
	Revision   uint64 `json:"revision,string"`
	Active     string `json:"active"`
	Previous   string `json:"previous"`
	Pending    string `json:"pending"`
}
type Inventory struct {
	State      State     `json:"state"`
	Installed  *Release  `json:"installed"`
	ObservedAt time.Time `json:"observed_at"`
}
type Activation struct {
	ExpectedRevision uint64
	ExecutionID      string
	ReleaseID        string
}
type Receipt struct {
	State       State     `json:"state"`
	Outcome     string    `json:"outcome"` // completed, restored, recovery_required, not_applied
	Ready       bool      `json:"ready"`
	CompletedAt time.Time `json:"completed_at"`
}
