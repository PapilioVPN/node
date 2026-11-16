// Package api reads native Xray runtime facts using a borrowed API resource.
package api

import (
	"context"
	stats "github.com/xtls/xray-core/app/stats/command"
)

type API interface {
	Begin(context.Context) (context.Context, func(), error)
	StatsClient() (stats.StatsServiceClient, error)
}

type Reader struct{ API API }
