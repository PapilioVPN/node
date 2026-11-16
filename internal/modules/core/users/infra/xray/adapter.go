// Package xray implements user capability ports using the native Xray API.
package xray

import (
	"context"

	native "github.com/xtls/xray-core/app/proxyman/command"
)

// API borrows the instance owner's shared admission, deadline and native channel.
// The owner closes it after transport drain; an Adapter never owns another pool.
type API interface {
	Begin(context.Context) (context.Context, func(), error)
	HandlerClient() (native.HandlerServiceClient, error)
}

type Adapter struct{ API API }
