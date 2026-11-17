// Package api implements routing capability ports using the native Xray API.
package api

import (
	"context"

	native "github.com/xtls/xray-core/app/router/command"
)

// API borrows the shared instance resource; the owner retains channel cleanup.
type API interface {
	Begin(context.Context) (context.Context, func(), error)
	RoutingClient() (native.RoutingServiceClient, error)
}

// Router implements routing management using one borrowed instance API resource.
type Router struct{ API API }
