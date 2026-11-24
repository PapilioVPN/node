package native

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
)

func (control *Control) UpsertPeer(ctx context.Context, owner lifecycle.Link, desired peers.Peer) (peers.Result, error) {
	snapshot, err := control.peerState(ctx, owner, []peers.Peer{desired})
	if err != nil {
		return peers.Result{}, err
	}
	change, result, err := peers.PlanPeer(snapshot.peers, desired)
	if err != nil {
		return peers.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return peers.Result{}, err
	}
	if result == (peers.Result{}) {
		return result, nil
	}
	if err := control.writePeerMessage(ctx, peerDeadline(ctx), owner.Index, []peers.Change{change}); err != nil {
		return peers.Result{}, errors.Join(peers.ErrUnconfirmed, err)
	}
	return result, nil
}

func (control *Control) RemovePeer(ctx context.Context, owner lifecycle.Link, key [32]byte) error {
	if err := control.checkPeerLink(ctx, owner); err != nil {
		return err
	}
	change := peers.Change{Peer: peers.Peer{PublicKey: key}, Remove: true}
	if err := control.writePeerMessage(ctx, peerDeadline(ctx), owner.Index, []peers.Change{change}); err != nil {
		return errors.Join(peers.ErrUnconfirmed, err)
	}
	return nil
}

func (control *Control) ApplyPeerDelta(ctx context.Context, owner lifecycle.Link, desired peers.Delta) (peers.Result, error) {
	snapshot, err := control.peerState(ctx, owner, desired.Upsert)
	if err != nil {
		return peers.Result{}, err
	}
	updates, removals, result, err := peers.PlanDelta(snapshot.peers, desired)
	if err != nil {
		return peers.Result{}, err
	}
	return control.applyPeerChanges(ctx, owner.Index, updates, removals, result)
}
