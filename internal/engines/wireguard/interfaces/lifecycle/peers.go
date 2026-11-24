package lifecycle

import (
	"context"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
)

func (service *Service) SyncPeers(ctx context.Context, name string, input []peers.Input) (peers.Result, error) {
	return service.withPeerLink(ctx, name, func(ctx context.Context, link Link) (peers.Result, error) {
		desired, err := peers.Prepare(input)
		if err != nil {
			return peers.Result{}, err
		}
		return service.control.SyncPeers(ctx, link, desired)
	})
}

func (service *Service) UpsertPeer(ctx context.Context, name string, input peers.Input) (peers.Result, error) {
	return service.withPeerLink(ctx, name, func(ctx context.Context, link Link) (peers.Result, error) {
		desired, err := peers.PreparePeer(input)
		if err != nil {
			return peers.Result{}, err
		}
		return service.control.UpsertPeer(ctx, link, desired)
	})
}

func (service *Service) RemovePeer(ctx context.Context, name string, input []byte) error {
	_, err := service.withPeerLink(ctx, name, func(ctx context.Context, link Link) (peers.Result, error) {
		key, err := peers.Key(input)
		if err != nil {
			return peers.Result{}, err
		}
		return peers.Result{}, service.control.RemovePeer(ctx, link, key)
	})
	return err
}

func (service *Service) ApplyPeerDelta(ctx context.Context, name string, input peers.DeltaInput) (peers.Result, error) {
	return service.withPeerLink(ctx, name, func(ctx context.Context, link Link) (peers.Result, error) {
		delta, err := peers.PrepareDelta(input)
		if err != nil {
			return peers.Result{}, err
		}
		return service.control.ApplyPeerDelta(ctx, link, delta)
	})
}

func (service *Service) withPeerLink(ctx context.Context, name string, operation func(context.Context, Link) (peers.Result, error)) (peers.Result, error) {
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
	return operation(ctx, current.Link)
}
