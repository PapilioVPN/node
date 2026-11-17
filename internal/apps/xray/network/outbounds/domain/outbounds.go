package domain

import (
	"errors"
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
)

const ReadOutboundStatus = "xray.outbound.status.read"
const ListOutboundStatuses = "xray.outbound.statuses.list"

var ErrOutboundObservationUnsupported = errors.New("Xray outbound observations are not supported")
var ErrInvalidOutboundObservation = errors.New("invalid Xray outbound observation")

// OutboundHealthPing retains the native burst sample statistics. Durations are
// signed nanoseconds; they are not the millisecond unit of OutboundStatus.DelayMS.
type OutboundHealthPing struct {
	TotalSamples  int64
	FailedSamples int64
	DeviationNS   int64
	AverageNS     int64
	MaxNS         int64
	MinNS         int64
}

// OutboundStatus is an engine-owned observation, not a new probe or a readiness
// guarantee. LastErrorReason is private native diagnostic data. Native timestamps
// can be zero (not provided by burst), and failed ordinary probes use a delay sentinel.
type OutboundStatus struct {
	Tag             string
	Alive           bool
	DelayMS         int64
	LastErrorReason string
	LastSeenUnixSec int64
	LastTryUnixSec  int64
	HealthPing      *OutboundHealthPing
}

type OutboundStatuses struct {
	Items      []OutboundStatus
	ObservedAt time.Time
}

type InstanceOutboundStatuses struct {
	InstanceID instances.InstanceID
	Statuses   OutboundStatuses
}

// OutboundObservation distinguishes a missing observation from a reported zero
// delay or unhealthy status. Absence does not establish outbound membership.
type OutboundObservation struct {
	Status     *OutboundStatus
	ObservedAt time.Time
}

type InstanceOutboundObservation struct {
	InstanceID  instances.InstanceID
	Tag         string
	Observation OutboundObservation
}
