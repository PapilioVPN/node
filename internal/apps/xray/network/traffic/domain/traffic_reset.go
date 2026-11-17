package domain

import (
	"errors"
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
)

const ResetTrafficCounter = "xray.traffic.counter.reset"

// ErrTrafficResetUnknown means an attempted reset may have changed the counter.
// Retrying can discard subsequent usage; a current read cannot recover lost bytes.
var ErrTrafficResetUnknown = errors.New("Xray traffic reset outcome is unknown")

// TrafficCounterReset acknowledges one atomic swap with zero, not a durable
// accounting transaction. Concurrent traffic can immediately increase the counter.
type TrafficCounterReset struct {
	PreviousBytes  int64
	AcknowledgedAt time.Time // Node's receipt time, not an engine-provided reset time.
}

type InstanceTrafficCounterReset struct {
	InstanceID instances.InstanceID
	Selection  TrafficSelection
	Reset      TrafficCounterReset
}
