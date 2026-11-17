package domain

import (
	"errors"
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
)

const ReadTrafficCounter = "xray.traffic.counter.read"
const ListTrafficCounters = "xray.traffic.counter.list"

var ErrInvalidTrafficSelection = errors.New("invalid Xray traffic selection")
var ErrTrafficCounterNotFound = errors.New("Xray traffic counter not found")
var ErrTrafficUnsupported = errors.New("Xray traffic counters are not supported")
var ErrInvalidTrafficResponse = errors.New("invalid Xray traffic counter response")

type TrafficScope string

const (
	UserTraffic     TrafficScope = "user"
	InboundTraffic  TrafficScope = "inbound"
	OutboundTraffic TrafficScope = "outbound"
)

type TrafficDirection string

const (
	Uplink   TrafficDirection = "uplink"
	Downlink TrafficDirection = "downlink"
)

// TrafficSelection identifies one counter. Name is an exact email or handler tag.
type TrafficSelection struct {
	Scope     TrafficScope
	Name      string
	Direction TrafficDirection
}

// TrafficCounter preserves native signed bytes, including overflow/reset effects.
// It is a current engine counter, not durable usage or a billing total.
type TrafficCounter struct {
	Bytes      int64
	ObservedAt time.Time
}

type InstanceTrafficCounter struct {
	InstanceID instances.InstanceID
	Selection  TrafficSelection
	Counter    TrafficCounter
}

// TrafficCounterEntry identifies one observed native counter within its scope.
type TrafficCounterEntry struct {
	Name      string
	Direction TrafficDirection
	Bytes     int64
}

// TrafficCounters owns its items. ObservedAt is collection completion, not an
// atomic snapshot time; native counters can change independently during the read.
type TrafficCounters struct {
	Items      []TrafficCounterEntry
	ObservedAt time.Time
}

type InstanceTrafficCounters struct {
	InstanceID instances.InstanceID
	Scope      TrafficScope
	Counters   TrafficCounters
}
