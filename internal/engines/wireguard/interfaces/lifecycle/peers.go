package lifecycle

import (
	"context"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
)

func (service *Service) SyncPeers(ctx context.Context, name string, input []peers.Input) (peers.Result, error) {
	if name != service.name {
		return peers.Result{}, status.ErrNotFound
	}
	if !service.mu.TryLock() {
		return peers.Result{}, status.ErrBusy
	}
	defer service.mu.Unlock()
	current := service.applied.Load()
	if current == nil {
		return peers.Result{}, status.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return peers.Result{}, err
	}
	desired, err := peers.Prepare(input)
	if err != nil {
		return peers.Result{}, err
	}
	return service.control.SyncPeers(ctx, current.Link, desired)
}
