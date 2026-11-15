// Package domain defines engine-independent instance identities and lifecycle observations.
package domain

import "errors"

var ErrNotFound = errors.New("instance not found")

type InstanceID string

type EngineKind string

const Xray EngineKind = "xray"

// Instance distinguishes a configured runtime identity from its engine kind.
type Instance struct {
	ID   InstanceID
	Kind EngineKind
}

type State string

const (
	Stopped  State = "stopped"
	Running  State = "running"
	Stopping State = "stopping"
	Exited   State = "exited"
)

// Status is an observed lifecycle state, not a readiness or traffic-health claim.
type Status struct {
	Instance Instance
	State    State
	// Failure is the last completed execution's native cause, independent of
	// whether reading status succeeded. Wire adapters must map it explicitly.
	Failure error
}

// Management capabilities describe implemented operations, not caller grants.
const (
	ReadStatus    = "instance.status.read"
	StartInstance = "instance.start"
	StopInstance  = "instance.stop"
)
