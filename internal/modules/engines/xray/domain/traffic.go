package domain

import (
	"errors"
	"strings"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/instances/domain"
)

const ReadTrafficCounter = "xray.traffic.counter.read"

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

// Validate checks boundary input without changing its identity.
func (s TrafficSelection) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return ErrInvalidTrafficSelection
	}
	if s.Scope != UserTraffic && s.Scope != InboundTraffic && s.Scope != OutboundTraffic {
		return ErrInvalidTrafficSelection
	}
	if s.Direction != Uplink && s.Direction != Downlink {
		return ErrInvalidTrafficSelection
	}
	return nil
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
