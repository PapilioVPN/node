package api

import (
	"context"

	stats "github.com/xtls/xray-core/app/stats/command"
)

// OnlineAPI borrows native statistics without owning the instance's channel.
type OnlineAPI interface {
	Begin(context.Context) (context.Context, func(), error)
	StatsClient() (stats.StatsServiceClient, error)
}

type Online struct{ API OnlineAPI }
