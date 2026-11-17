package domain

import instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"

const ReadTrafficPair = "xray.traffic.pair.read"
const ResetTrafficPair = "xray.traffic.pair.reset"

// TrafficPairSelection identifies both directions of one exact email or tag.
type TrafficPairSelection struct {
	Scope TrafficScope
	Name  string
}

// TrafficPair contains independent observations, never an atomic snapshot.
// A nil direction is absent; a present direction can have zero or signed bytes.
type TrafficPair struct {
	Uplink   *TrafficCounter
	Downlink *TrafficCounter
}

type InstanceTrafficPair struct {
	InstanceID instances.InstanceID
	Selection  TrafficPairSelection
	Pair       TrafficPair
}

// InstanceTrafficPairReset retains both planned directions, uplink then downlink.
// Missing counters are known not applied; other errors can leave partial effects.
type InstanceTrafficPairReset struct {
	InstanceID instances.InstanceID
	Selection  TrafficPairSelection
	Items      [2]TrafficCounterResetEntry
}
