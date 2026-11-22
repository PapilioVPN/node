// Package domain defines scoped account reconciliation and credential-free receipts.
package domain

import (
	"errors"
	"time"

	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

var (
	ErrInvalid     = errors.New("invalid sync input")
	ErrCapacity    = errors.New("sync capacity exceeded")
	ErrStale       = errors.New("sync revision or plan changed")
	ErrConflict    = errors.New("sync identity already used for different input")
	ErrNotFound    = errors.New("sync operation not retained")
	ErrUnsupported = errors.New("sync operation unsupported")
	ErrUncertain   = errors.New("reconcile uncertain sync before another operation")
)

const (
	MaxInbounds      = 128
	MaxUsers         = 65536
	MaxBytes         = 32 << 20
	MaxChunkItems    = 50
	MaxChunkBytes    = 1 << 20
	MaxTransfers     = 8
	MaxRetainedBytes = 32 << 20
	Lifetime         = 30 * time.Second
)

type Mode string

const (
	Delta Mode = "delta"
	Full  Mode = "full"
)

// Revision binds a review to desired config and the observed execution. Epoch
// and Control belong to the sync owner and expire when Node restarts.
type Revision struct {
	Epoch      string
	Control    uint64
	Config     uint64
	Generation uint64
}

// Request contains native account material. Never encode this as a preview,
// error, status or log. Every selected full set must be explicitly present;
// a non-nil empty Accounts slice clears only that inbound.
type Request struct {
	InstanceID  string
	OperationID string
	Expected    Revision
	Mode        Mode
	Selection   []string
	Sets        []accounts.InboundUserSet
	Removals    []accounts.Removal
}

type Member struct {
	Email         string
	AccountType   string
	Configuration []byte
	// Comparable is false for lossy native readback (notably SS and WireGuard).
	Comparable  bool
	Fingerprint [32]byte
}

type Inbound struct {
	Tag           string
	AccountType   string
	EmailMatching string
	Live          bool
	Users         []Member
}
type Snapshot struct {
	Revision Revision
	Inbounds []Inbound
}

type Action string

const (
	Add    Action = "add"
	Remove Action = "remove"
)

// Step contains private input. Use StepResult for external evidence.
type Step struct {
	InboundTag    string
	Email         string
	Action        Action
	Configuration []byte
}
type Difference struct {
	InboundTag string
	Added      int
	Removed    int
	Replaced   int
	Unchanged  int
}
type Preview struct {
	PlanID      string
	InstanceID  string
	OperationID string
	Revision    Revision
	Mode        Mode
	ApplyMode   string
	Reason      string
	Differences []Difference
	ExpiresAt   time.Time
}

// Plan stays inside the application. Credentials and comparison fingerprints
// are deliberately absent from Preview.
type Plan struct {
	Preview  Preview
	Steps    []Step
	Request  Request
	Snapshot Snapshot
}

type StepResult struct {
	InboundTag      string
	Email           string
	Action          Action
	Attempted       bool
	Effect          accounts.Effect
	Acknowledged    bool
	RequiresRestart bool
	// Code is sanitized; private native causes are returned separately.
	Code string
}
type Receipt struct {
	InstanceID       string
	OperationID      string
	Revision         Revision
	Effect           accounts.Effect
	InFlight         bool
	Durable          bool
	Ready            bool
	RequiresRecovery bool
	CompletedAt      time.Time
	Steps            []StepResult
}
type ReceiptPage struct {
	Receipt    Receipt
	NextOffset int
}
