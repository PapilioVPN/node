package domain

import instancedomain "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"

// Change reports independent persistence and execution phases. A successful
// execution acknowledgement means spawning, not API readiness or proxy traffic.
type Change struct {
	Persistence Persistence
	Execution   *instancedomain.Control
}
