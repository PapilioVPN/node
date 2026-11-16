package domain

import (
	"errors"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
)

const ListRoutingRules = "xray.routing.rules.list"
const ReadBalancerInfo = "xray.routing.balancer.read"

var ErrRoutingReadUnsupported = errors.New("Xray routing observations are not supported")
var ErrRoutingTargetNotFound = errors.New("Xray routing target not found")
var ErrInvalidRoutingResponse = errors.New("invalid Xray routing response")
var ErrInvalidBalancerTag = errors.New("invalid Xray balancer tag")

// RoutingRuleReference exposes only native ListRule metadata. Order is routing
// priority; empty tags and duplicates remain intact. Conditions are not provided.
type RoutingRuleReference struct {
	OutboundTag string
	RuleTag     string
}

type RoutingRules struct {
	Items      []RoutingRuleReference
	ObservedAt time.Time
}

type BalancerTargets struct{ Tags []string }

// BalancerInfo preserves native field presence. An empty reported override is
// distinct from unavailable override information. Principal targets are strategy
// observations, not a complete configured selector or a future routing guarantee.
// The native service can omit targets after an internally suppressed lookup error.
type BalancerInfo struct {
	OverrideTarget   *string
	PrincipalTargets *BalancerTargets
	ObservedAt       time.Time
}

type InstanceRoutingRules struct {
	InstanceID instances.InstanceID
	Rules      RoutingRules
}

type InstanceBalancerInfo struct {
	InstanceID instances.InstanceID
	Tag        string
	Balancer   BalancerInfo
}
