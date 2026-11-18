// Package domain contains network interface observations, independent of I/O.
package domain

import "time"

// Interface identifiers and flags belong to the current network namespace.
// Index/name can be reused after removal; neither is a durable public identity.
type Interface struct {
	Index            uint32
	Name             string
	Kind             string
	MTU              uint32
	HardwareAddress  string
	Flags            uint32
	OperationalState uint32
	// A parent may belong to a peer namespace; nil namespace ID means unreported.
	ParentNamespaceID *int32
	ParentIndex       uint32
	MasterIndex       uint32
	Addresses         []Address
}

type Address struct {
	InterfaceIndex uint32
	Family         string
	LocalPrefix    string
	PeerPrefix     *string
	Broadcast      *string
	Label          string
	Flags          uint32
	Scope          uint32
	// Linux reports MaxUint32 for an unlimited lifetime.
	PreferredLifetimeSeconds uint32
	ValidLifetimeSeconds     uint32
}

// Snapshot joins two non-atomic dumps by interface index. Addresses whose link
// was not in the earlier dump remain explicit rather than being silently lost.
type Snapshot struct {
	Interfaces         []Interface
	UnmatchedAddresses []Address
}

type Observation struct {
	Snapshot   Snapshot
	ObservedAt time.Time
	Duration   time.Duration
}

type InterfaceCounters struct {
	Index uint32
	Name  string
	// nil means no standard counters were supplied, not known zero traffic.
	Counters *Counters
}

type CounterObservation struct {
	Interfaces []InterfaceCounters
	ObservedAt time.Time
	Duration   time.Duration
}
