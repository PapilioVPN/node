// Package domain describes live routing observations and ephemeral native effects.
package domain

import (
	"errors"
	"net/netip"
	"time"
)

var (
	ErrInvalidArgument    = errors.New("invalid routing input")
	ErrNotFound           = errors.New("Xray instance not found")
	ErrUnsupported        = errors.New("routing facility not configured or implemented")
	ErrNotRunning         = errors.New("Xray instance is not running")
	ErrCapacity           = errors.New("routing capacity exceeded")
	ErrUncertain          = errors.New("replace uncertain execution before routing requests")
	ErrExecutionChanged   = errors.New("execution changed during routing operation")
	ErrObservationExpired = errors.New("routing observation expired")
	ErrInvalidResponse    = errors.New("invalid native routing response")
)

const (
	MaxRuleBytes        = 1 << 20
	MaxNativeBytes      = 32 << 20
	MaxRecords          = 65536
	MaxPageSize         = 50
	MaxObservations     = 8
	MaxObservationBytes = 4 << 20
	ObservationTTL      = 30 * time.Second
)

type Effect string

const (
	NotApplied Effect = "not_applied"
	Completed  Effect = "completed"
	Unknown    Effect = "unknown"
)

type Mode string

const (
	Append  Mode = "append"
	Replace Mode = "replace"
)

type Network int32

const (
	UnknownNetwork Network = 0
	TCP            Network = 2
	UDP            Network = 3
	UNIX           Network = 4
)

// Context is a native routing input or projection. Omitted projected fields carry
// native zeros; the native projection does not return VLESSRoute.
type Context struct {
	InboundTag        string
	Network           Network
	SourceIPs         []netip.Addr
	TargetIPs         []netip.Addr
	LocalIPs          []netip.Addr
	SourcePort        uint16
	TargetPort        uint16
	LocalPort         uint16
	VLESSRoute        uint16
	TargetDomain      string
	Protocol          string
	User              string
	Attributes        map[string]string
	OutboundGroupTags []string
	OutboundTag       string
}
type ProbeRequest struct {
	InstanceID     string
	Context        Context
	FieldSelectors []string
	PublishResult  bool
}
type AddRequest struct {
	InstanceID         string
	RuleJSON           []byte
	Mode               Mode
	ConfirmDestructive bool
}
type OverrideRequest struct {
	InstanceID, BalancerTag string
	Target                  *string
}
type Rule struct {
	OutboundTag string
	RuleTag     string
}
type Balancer struct {
	Override         *string
	PrincipalTargets *[]string
}
type NativeResult struct {
	Effect                  Effect
	Acknowledged            bool
	RequiresInstanceRestart bool
}

// Operation is volatile request evidence, not a durable journal or proof of
// outbound traffic. A native failure can leave partial engine effects.
type Operation struct {
	InstanceID              string
	Kind                    string
	Generation              uint64
	Effect                  Effect
	Acknowledged            bool
	RequiresInstanceRestart bool
	ObservedAt              time.Time
	Duration                time.Duration
}
type Observation struct {
	InstanceID string
	Generation uint64
	ObservedAt time.Time
	Duration   time.Duration
}
type ProbeResult struct {
	Operation Operation
	Route     *Context
}
type BalancerResult struct {
	Observation Observation
	Tag         string
	Balancer    Balancer
}
type PageRequest struct {
	InstanceID string
	PageSize   int
	PageToken  string
}
type Page struct {
	Observation
	Rules                        []Rule
	ObservationID, NextPageToken string
	ExpiresAt, ServedAt          time.Time
	Total                        int
	Fresh                        bool
}
type ControlStatus struct {
	Observation
	ExecutionState                                          string
	APIConfigured, RequestInFlight, RequiresInstanceRestart bool
	LastOperation                                           *Operation
	// The official standalone registration passes a nil native stats channel.
	RoutingStatsStream string
}
