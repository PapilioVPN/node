package domain

import (
	"errors"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
)

const OverrideBalancerTarget = "xray.routing.balancer.override"
const RemoveRoutingRule = "xray.routing.rule.remove"
const AddRoutingRule = "xray.routing.rule.add"

var ErrInvalidBalancerOverride = errors.New("invalid Xray balancer override")
var ErrInvalidRoutingRuleTag = errors.New("invalid Xray routing rule tag")
var ErrRoutingManagementUnsupported = errors.New("Xray routing management is not supported")
var ErrRoutingMutationUnknown = errors.New("Xray routing mutation effects are unknown")
var ErrInvalidRoutingRuleMode = errors.New("invalid Xray routing rule mode")
var ErrInvalidRoutingRule = errors.New("invalid Xray routing rule")
var ErrInvalidPreparedRoutingRule = errors.New("invalid prepared Xray routing rule")
var ErrRoutingRulePreparationFailed = errors.New("Xray routing rule preparation failed before application")
var ErrRoutingRulePreparationDisabled = errors.New("Xray routing rule preparation is not enabled")
var ErrRoutingRulePreparationBusy = errors.New("Xray routing rule preparation is busy")
var ErrRoutingRuleInputLimit = errors.New("Xray routing rule input exceeds its limit")
var ErrRoutingRuleOutputLimit = errors.New("Xray prepared routing rule exceeds its limit")

type RoutingRuleMode uint8

const (
	AppendRoutingRule RoutingRuleMode = iota + 1
	ReplaceRoutingRules
)

// RoutingRuleAddition borrows one native routing.rules[] JSON object. Replace
// replaces the native rules and balancers; it does not prepend a single rule.
type RoutingRuleAddition struct {
	JSON []byte
	Mode RoutingRuleMode
}

// PreparedRoutingRule owns one canonical native router.Config protobuf, produced
// by the selected instance's builder. It is not a committed configuration revision.
type PreparedRoutingRule struct {
	Config  []byte
	RuleTag string
}

type InstanceRoutingRuleAddition struct {
	InstanceID      instances.InstanceID
	RuleTag         string
	Mode            RoutingRuleMode
	Acknowledgement RoutingMutationAcknowledgement
}

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
