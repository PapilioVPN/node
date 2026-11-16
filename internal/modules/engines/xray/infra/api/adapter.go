// Package api adapts native Xray capabilities using an instance-owned API resource.
package api

import (
	"context"

	observatory "github.com/xtls/xray-core/app/observatory/command"
	routing "github.com/xtls/xray-core/app/router/command"
	stats "github.com/xtls/xray-core/app/stats/command"
)

// API borrows the resource owner's admission, deadlines and cached native stubs.
// The owner closes the shared channel after transport drain; Adapter owns no pool.
type API interface {
	Begin(context.Context) (context.Context, func(), error)
	StatsClient() (stats.StatsServiceClient, error)
	RoutingClient() (routing.RoutingServiceClient, error)
	ObservatoryClient() (observatory.ObservatoryServiceClient, error)
}

type Adapter struct{ API API }
