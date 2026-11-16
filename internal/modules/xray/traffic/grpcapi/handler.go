// Package grpcapi adapts traffic operations to the existing versioned wire contract.
package grpcapi

// TrafficAPI is composed into the existing XrayService registration.
type TrafficAPI struct {
	Traffic         TrafficCounterReader
	Resets          TrafficCounterResetter
	ScopeResets     TrafficCountersResetter
	Pairs           TrafficPairReader
	PairResets      TrafficPairResetter
	TrafficCounters TrafficCountersReader
	AllowStatsRead  bool
	AllowStatsReset bool
}
