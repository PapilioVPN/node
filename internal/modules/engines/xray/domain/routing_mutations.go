package domain

import (
	"errors"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
)

const OverrideBalancerTarget = "xray.routing.balancer.override"
const RemoveRoutingRule = "xray.routing.rule.remove"

var ErrInvalidBalancerOverride = errors.New("invalid Xray balancer override")
var ErrInvalidRoutingRuleTag = errors.New("invalid Xray routing rule tag")
var ErrRoutingManagementUnsupported = errors.New("Xray routing management is not supported")
var ErrRoutingMutationUnknown = errors.New("Xray routing mutation effects are unknown")

// BalancerOverride assigns an opaque target without checking outbound existence
// or selector membership. Only an exactly empty target clears the override and
// resumes the existing native strategy; whitespace is an ordinary target.
type BalancerOverride struct {
	BalancerTag string
	Target      string
}

// RoutingMutationAcknowledgement records Node receipt of native completion, not
// persistence, a configuration revision or state fenced against other writers.
type RoutingMutationAcknowledgement struct{ AcknowledgedAt time.Time }

type InstanceBalancerOverride struct {
	InstanceID      instances.InstanceID
	Override        BalancerOverride
	Acknowledgement RoutingMutationAcknowledgement
}

// InstanceRoutingRuleRemoval acknowledges native completion, including a no-op
// for a missing tag. It carries neither a removal count nor durable config state.
type InstanceRoutingRuleRemoval struct {
	InstanceID      instances.InstanceID
	RuleTag         string
	Acknowledgement RoutingMutationAcknowledgement
}
