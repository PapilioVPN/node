// Package api implements user capability ports using the native Xray API.
package api

import (
	"context"
	"sync"

	native "github.com/xtls/xray-core/app/proxyman/command"

	"github.com/PapilioVPN/node/internal/shared/engineapi"
)

// API borrows the instance owner's shared admission, deadline and native channel.
// The owner closes it after transport drain; an Adapter never owns another pool.
type API interface {
	Begin(context.Context) (context.Context, func(), error)
	HandlerClient() (native.HandlerServiceClient, error)
}

type Adapter struct {
	API    API
	writes sync.Mutex
}

// beginMutation serializes this adapter's admitted user writes without a queue.
// Reads retain independent shared native capacity. This is operation admission,
// not a state lock; it covers all phases of a write and releases on every exit.
func (c *Adapter) beginMutation(ctx context.Context) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if !c.writes.TryLock() {
		return nil, nil, engineapi.ErrBusy
	}
	ctx, done, err := c.API.Begin(ctx)
	if err != nil {
		c.writes.Unlock()
		return nil, nil, err
	}
	return ctx, func() { done(); c.writes.Unlock() }, nil
}
