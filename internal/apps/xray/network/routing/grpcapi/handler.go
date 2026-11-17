// Package grpcapi adapts routing operations to the existing versioned wire contract.
package grpcapi

// RoutingAPI is composed into the existing XrayService registration.
type RoutingAPI struct {
	Routing                RoutingReader
	Probes                 RoutingProber
	BalancerOverrides      BalancerOverrider
	RuleRemoval            RoutingRuleRemover
	RuleAddition           RoutingRuleAdder
	AllowRoutingRead       bool
	AllowRoutingTest       bool
	AllowRoutingManagement bool
}
