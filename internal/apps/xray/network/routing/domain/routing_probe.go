package domain

import (
	"errors"
	"net/netip"
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
)

const TestRoute = "xray.routing.test"

var ErrInvalidRoutingProbe = errors.New("invalid Xray routing probe")
var ErrRoutingProbeUnsupported = errors.New("Xray routing probes are not supported")
var ErrRoutingProbeUnknown = errors.New("Xray routing probe effects are unknown")
var ErrInvalidRoutingProbeResponse = errors.New("invalid Xray routing probe response")

type RoutingNetwork uint8

const (
	RoutingUnknown RoutingNetwork = iota
	RoutingTCP
	RoutingUDP
	RoutingUnix
)

// RoutingContext contains the facts supplied to native route selection. Ports
// include zero; IP order, duplicates, IPv4-mapped IPv6 and opaque strings survive.
// Addresses must be valid and unzoned because native IP bytes cannot encode zones.
type RoutingContext struct {
	InboundTag   string
	Network      RoutingNetwork
	SourceIPs    []netip.Addr
	SourcePort   uint16
	TargetIPs    []netip.Addr
	TargetPort   uint16
	TargetDomain string
	Protocol     string
	User         string
	Attributes   map[string]string
	LocalIPs     []netip.Addr
	LocalPort    uint16
	VLESSRoute   uint16
}

// RoutingProbe borrows its context and selectors synchronously. Native selectors
// are prefixes (including empty/unknown prefixes), not an enumerated allowlist.
// Route selection can advance a balancer, resolve DNS and fire configured webhooks
// even when PublishResult is false. It never rewrites the configuration.
type RoutingProbe struct {
	Context        RoutingContext
	FieldSelectors []string
	PublishResult  bool
}

// RouteResult owns native projected fields. Omitted fields retain native zero
// values; do not infer facts or echo input for fields excluded by projection.
// The pinned engine does not populate VLESSRoute in its result projection.
type RouteResult struct {
	Context           RoutingContext
	OutboundTag       string
	OutboundGroupTags []string
}

// RoutingProbeResult acknowledges native RPC completion, not webhook completion,
// routing-stat delivery, persistent state or the outcome of a future connection.
type RoutingProbeResult struct {
	Route          RouteResult
	AcknowledgedAt time.Time
}

type InstanceRoutingProbe struct {
	InstanceID instances.InstanceID
	Result     RoutingProbeResult
}
