package lifecycle

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
)

// PeerControl reads and applies changes to the kernel link under the lifecycle
// owner's serialization. Read and write calls follow a successful link check.
type PeerControl interface {
	CheckPeerLink(context.Context, Link) error
	ReadPeerSet(context.Context, Link) (peers.Snapshot, error)
	WritePeer(context.Context, Link, peers.Change) error
	WritePeerChanges(context.Context, Link, []peers.Change, []peers.Change) error
}

func (service *Service) SyncPeers(ctx context.Context, name string, input []peers.Input) (peers.Result, error) {
	return service.withPeerLink(ctx, name, func(ctx context.Context, link Link) (peers.Result, error) {
		desired, err := peers.Prepare(input)
		if err != nil {
			return peers.Result{}, err
		}
		service.peerState = nil
		if err := service.peers.CheckPeerLink(ctx, link); err != nil {
			return peers.Result{}, err
		}
		snapshot, err := service.peers.ReadPeerSet(ctx, link)
		if err != nil {
			return peers.Result{}, err
		}
		for _, peer := range desired {
			if peer.PublicKey == snapshot.PublicKey {
				return peers.Result{}, peers.ErrInvalid
			}
		}
		updates, removals, result := peers.Diff(snapshot.Peers, desired)
		if err := service.writePeerChanges(ctx, link, updates, removals); err != nil {
			return peers.Result{}, err
		}
		return result, nil
	})
}

func (service *Service) UpsertPeer(ctx context.Context, name string, input peers.Input) (peers.Result, error) {
	return service.withPeerLink(ctx, name, func(ctx context.Context, link Link) (peers.Result, error) {
		desired, err := peers.PreparePeer(input)
		if err != nil {
			return peers.Result{}, err
		}
		state, err := service.confirmedPeers(ctx, link)
		if err != nil {
			return peers.Result{}, err
		}
		change, result, err := state.PlanPeer(desired)
		if err != nil {
			return peers.Result{}, err
		}
		if err := ctx.Err(); err != nil {
			return peers.Result{}, err
		}
		if result == (peers.Result{}) {
			return result, nil
		}
		if err := service.writePeer(ctx, link, change); err != nil {
			return peers.Result{}, err
		}
		state.CommitPeer(change)
		return result, nil
	})
}

func (service *Service) RemovePeer(ctx context.Context, name string, input []byte) error {
	_, err := service.withPeerLink(ctx, name, func(ctx context.Context, link Link) (peers.Result, error) {
		key, err := peers.Key(input)
		if err != nil {
			return peers.Result{}, err
		}
		if err := service.checkPeerLink(ctx, link); err != nil {
			return peers.Result{}, err
		}
		change := peers.Change{Peer: peers.Peer{PublicKey: key}, Remove: true}
		if err := service.writePeer(ctx, link, change); err != nil {
			return peers.Result{}, err
		}
		if service.peerState != nil {
			service.peerState.Delete(key)
		}
		return peers.Result{}, nil
	})
	return err
}

func (service *Service) ApplyPeerDelta(ctx context.Context, name string, input peers.DeltaInput) (peers.Result, error) {
	return service.withPeerLink(ctx, name, func(ctx context.Context, link Link) (peers.Result, error) {
		delta, err := peers.PrepareDelta(input)
		if err != nil {
			return peers.Result{}, err
		}
		state, err := service.confirmedPeers(ctx, link)
		if err != nil {
			return peers.Result{}, err
		}
		updates, removals, result, err := state.PlanDelta(delta)
		if err != nil {
			return peers.Result{}, err
		}
		if err := service.writePeerChanges(ctx, link, updates, removals); err != nil {
			return peers.Result{}, err
		}
		state.CommitDelta(updates, removals)
		return result, nil
	})
}

func (service *Service) confirmedPeers(ctx context.Context, link Link) (*peers.State, error) {
	if err := service.checkPeerLink(ctx, link); err != nil {
		return nil, err
	}
	if service.peerState == nil {
		snapshot, err := service.peers.ReadPeerSet(ctx, link)
		if err != nil {
			return nil, err
		}
		service.peerState = peers.NewState(snapshot)
	}
	return service.peerState, nil
}

func (service *Service) checkPeerLink(ctx context.Context, link Link) error {
	if err := service.peers.CheckPeerLink(ctx, link); err != nil {
		service.peerState = nil
		return err
	}
	return nil
}

func (service *Service) writePeer(ctx context.Context, link Link, change peers.Change) error {
	if err := service.peers.WritePeer(ctx, link, change); err != nil {
		service.peerState = nil
		return errors.Join(peers.ErrUnconfirmed, err)
	}
	return nil
}

func (service *Service) writePeerChanges(ctx context.Context, link Link, updates, removals []peers.Change) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(updates)+len(removals) == 0 {
		return nil
	}
	if err := service.peers.WritePeerChanges(ctx, link, updates, removals); err != nil {
		service.peerState = nil
		return errors.Join(peers.ErrUnconfirmed, err)
	}
	return nil
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
