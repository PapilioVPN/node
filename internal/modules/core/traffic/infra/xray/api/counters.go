// Package api adapts Xray traffic counters using an instance-owned API resource.
package api

import (
	"context"

	native "github.com/xtls/xray-core/app/stats/command"
)

// API borrows admission, deadlines and the cached statistics stub. Its resource
// owner closes the shared channel after transport drain; Counters owns no pool.
type API interface {
	Begin(context.Context) (context.Context, func(), error)
	StatsClient() (native.StatsServiceClient, error)
}

type Counters struct{ API API }
