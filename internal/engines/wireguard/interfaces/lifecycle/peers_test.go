package lifecycle

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
)

func TestManagedPeerStateReadsAfterAnUnconfirmedWrite(t *testing.T) {
	first, second := [32]byte{1}, [32]byte{2}
	control := &peerControlFixture{snapshot: peers.Snapshot{PublicKey: [32]byte{9}}}
	service := New("wg0", nil, control, nil)
	service.applied.Store(&Checkpoint{Link: Link{Name: "wg0", Index: 3, Alias: "fixture"}})
	put := func(key [32]byte, prefix string) (peers.Result, error) {
		return service.UpsertPeer(t.Context(), "wg0", peers.Input{PublicKey: key[:], AllowedIPs: []string{prefix}})
	}
	if got, err := put(first, "192.0.2.1/32"); err != nil || got.Added != 1 {
		t.Fatalf("first peer = %+v, %v", got, err)
	}
	if got, err := put(first, "192.0.2.1/32"); err != nil || got != (peers.Result{}) || control.reads != 1 || control.writes != 1 {
		t.Fatalf("replay = %+v, %v with %d reads and %d writes", got, err, control.reads, control.writes)
	}
	if _, err := put(second, "192.0.2.1/32"); !errors.Is(err, peers.ErrPrefixConflict) || control.writes != 1 {
		t.Fatalf("conflicting claim = %v, writes = %d", err, control.writes)
	}
	control.loseAck = true
	if _, err := put(first, "192.0.2.2/32"); !errors.Is(err, peers.ErrUnconfirmed) {
		t.Fatalf("unacknowledged update = %v", err)
	}
	if got, err := put(first, "192.0.2.2/32"); err != nil || got != (peers.Result{}) || control.reads != 2 || control.writes != 2 {
		t.Fatalf("recovery = %+v, %v with %d reads and %d writes", got, err, control.reads, control.writes)
	}
	if _, err := put(second, "192.0.2.2/32"); !errors.Is(err, peers.ErrPrefixConflict) {
		t.Fatalf("recovered prefix claim = %v", err)
	}
	if got, err := put(second, "192.0.2.1/32"); err != nil || got.Added != 1 || control.reads != 2 {
		t.Fatalf("released prefix = %+v, %v with %d reads", got, err, control.reads)
	}
}

func TestManagedPeerStateRecoversAPartialDelta(t *testing.T) {
	first, second := [32]byte{1}, [32]byte{2}
	prefix := netip.MustParsePrefix("192.0.2.1/32")
	control := &peerControlFixture{snapshot: peers.Snapshot{PublicKey: [32]byte{9}, Peers: []peers.Peer{
		{PublicKey: first, AllowedIPs: []netip.Prefix{prefix}},
	}}}
	service := New("wg0", nil, control, nil)
	service.applied.Store(&Checkpoint{Link: Link{Name: "wg0", Index: 3, Alias: "fixture"}})
	delta := peers.DeltaInput{Upsert: []peers.Input{{PublicKey: second[:], AllowedIPs: []string{prefix.String()}}}, Remove: [][]byte{first[:]}}
	control.loseAck = true
	if _, err := service.ApplyPeerDelta(t.Context(), "wg0", delta); !errors.Is(err, peers.ErrUnconfirmed) {
		t.Fatalf("partial delta returned %v", err)
	}
	if got, err := service.ApplyPeerDelta(t.Context(), "wg0", delta); err != nil || got != (peers.Result{Added: 1}) || control.reads != 2 {
		t.Fatalf("retry = %+v, %v with %d reads", got, err, control.reads)
	}
	if got, err := service.ApplyPeerDelta(t.Context(), "wg0", delta); err != nil || got != (peers.Result{}) || control.reads != 2 || control.writes != 2 {
		t.Fatalf("replay = %+v, %v with %d reads and %d writes", got, err, control.reads, control.writes)
	}
}

func TestManagedFullSyncReplaysAndReconciles(t *testing.T) {
	first, second := [32]byte{1}, [32]byte{2}
	prefix := netip.MustParsePrefix("192.0.2.1/32")
	control := &peerControlFixture{snapshot: peers.Snapshot{PublicKey: [32]byte{9}, Peers: []peers.Peer{
		{PublicKey: first, AllowedIPs: []netip.Prefix{prefix}}, {PublicKey: second},
	}}}
	service := New("wg0", nil, control, nil)
	service.applied.Store(&Checkpoint{Link: Link{Name: "wg0", Index: 3, Alias: "fixture"}})
	input := []peers.Input{{PublicKey: first[:], AllowedIPs: []string{prefix.String()}}}
	if got, err := service.SyncPeers(t.Context(), "wg0", input, false); err != nil || got != (peers.Result{Removed: 1}) {
		t.Fatalf("initial sync = %+v, %v", got, err)
	}
	control.snapshot.Peers[0].AllowedIPs = []netip.Prefix{netip.MustParsePrefix("192.0.2.2/32")}
	if got, err := service.SyncPeers(t.Context(), "wg0", input, false); err != nil || got != (peers.Result{}) || control.reads != 1 || control.writes != 1 {
		t.Fatalf("managed replay = %+v, %v, reads %d, writes %d", got, err, control.reads, control.writes)
	}
	if got, err := service.SyncPeers(t.Context(), "wg0", input, true); err != nil || got != (peers.Result{Updated: 1}) || control.reads != 2 {
		t.Fatalf("reconcile = %+v, %v, reads %d", got, err, control.reads)
	}
	if got, err := service.SyncPeers(t.Context(), "wg0", []peers.Input{}, false); err != nil || got != (peers.Result{Removed: 1}) || control.reads != 2 {
		t.Fatalf("managed clear = %+v, %v, reads %d", got, err, control.reads)
	}
}

type peerControlFixture struct {
	snapshot      peers.Snapshot
	reads, writes int
	loseAck       bool
}

func (*peerControlFixture) CheckPeerLink(context.Context, Link) error { return nil }

func (control *peerControlFixture) ReadPeerSet(context.Context, Link) (peers.Snapshot, error) {
	control.reads++
	return control.snapshot, nil
}

func (control *peerControlFixture) WritePeerChanges(ctx context.Context, link Link, updates, removals []peers.Change) error {
	for _, changes := range [][]peers.Change{removals, updates} {
		for _, change := range changes {
			if err := control.WritePeer(ctx, link, change); err != nil {
				return err
			}
		}
	}
	return nil
}

func (control *peerControlFixture) WritePeer(_ context.Context, _ Link, change peers.Change) error {
	control.writes++
	peer := change.Peer
	peer.AllowedIPs = append([]netip.Prefix(nil), peer.AllowedIPs...)
	index := len(control.snapshot.Peers)
	for i := range control.snapshot.Peers {
		if control.snapshot.Peers[i].PublicKey == peer.PublicKey {
			index = i
			break
		}
	}
	switch {
	case change.Remove && index < len(control.snapshot.Peers):
		control.snapshot.Peers = append(control.snapshot.Peers[:index], control.snapshot.Peers[index+1:]...)
	case change.Remove:
	case index == len(control.snapshot.Peers):
		control.snapshot.Peers = append(control.snapshot.Peers, peer)
	default:
		control.snapshot.Peers[index] = peer
	}
	if control.loseAck {
		control.loseAck = false
		return context.DeadlineExceeded
	}
	return nil
}
