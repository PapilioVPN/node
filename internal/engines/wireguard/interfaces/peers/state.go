package peers

import (
	"crypto/sha256"
	"net/netip"
	"slices"
)

// Snapshot is the peer configuration observed on one live device.
type Snapshot struct {
	PublicKey [32]byte
	Peers     []Peer
}

type confirmedPeer struct {
	prefixes []netip.Prefix
	pskHash  [32]byte
}

// State indexes one managed execution's observed peers and acknowledged writes.
// Its lifecycle owner serializes access and discards it after an unknown write.
type State struct {
	self     [32]byte
	byKey    map[[32]byte]confirmedPeer
	owners   map[netip.Prefix][32]byte
	lastSync [32]byte
}

func NewState(snapshot Snapshot) *State {
	prefixes := 0
	for _, peer := range snapshot.Peers {
		prefixes += len(peer.AllowedIPs)
	}
	state := &State{
		self: snapshot.PublicKey, byKey: make(map[[32]byte]confirmedPeer, len(snapshot.Peers)),
		owners: make(map[netip.Prefix][32]byte, prefixes),
	}
	for _, peer := range snapshot.Peers {
		state.put(peer)
	}
	return state
}

func (state *State) MatchesSync(fingerprint [32]byte) bool {
	return fingerprint != [32]byte{} && state.lastSync == fingerprint
}

func (state *State) MarkSync(fingerprint [32]byte) {
	state.lastSync = fingerprint
}

func (state *State) PlanSync(desired []Peer) (updates, removals []Change, result Result, err error) {
	for _, peer := range desired {
		if peer.PublicKey == state.self {
			return nil, nil, Result{}, ErrInvalid
		}
		change, count := state.plan(peer)
		if count == (Result{}) {
			continue
		}
		updates = append(updates, change)
		result.Added += count.Added
		result.Updated += count.Updated
	}
	if len(desired)-int(result.Added) == len(state.byKey) {
		return updates, nil, result, nil
	}
	wanted := make(map[[32]byte]bool, len(desired))
	for _, peer := range desired {
		wanted[peer.PublicKey] = true
	}
	for key := range state.byKey {
		if !wanted[key] {
			removals = append(removals, Change{Peer: Peer{PublicKey: key}, Remove: true})
			result.Removed++
		}
	}
	return updates, removals, result, nil
}

func (state *State) PlanPeer(desired Peer) (Change, Result, error) {
	if desired.PublicKey == state.self {
		return Change{}, Result{}, ErrInvalid
	}
	change, result := state.plan(desired)
	if result.Added != 0 && len(state.byKey) >= MaxPeers {
		return Change{}, Result{}, ErrCapacity
	}
	if change.ReplaceAllowedIPs {
		for _, prefix := range desired.AllowedIPs {
			if owner, exists := state.owners[prefix]; exists && owner != desired.PublicKey {
				return Change{}, Result{}, ErrPrefixConflict
			}
		}
	}
	return change, result, nil
}

func (state *State) PlanDelta(desired Delta) (updates, removals []Change, result Result, err error) {
	var touched map[[32]byte]bool
	for _, peer := range desired.Upsert {
		if peer.PublicKey == state.self {
			return nil, nil, Result{}, ErrInvalid
		}
		change, count := state.plan(peer)
		if count == (Result{}) {
			continue
		}
		if change.ReplaceAllowedIPs {
			for _, prefix := range peer.AllowedIPs {
				owner, exists := state.owners[prefix]
				if !exists || owner == peer.PublicKey {
					continue
				}
				if touched == nil {
					touched = deltaKeys(desired)
				}
				if !touched[owner] {
					return nil, nil, Result{}, ErrPrefixConflict
				}
			}
		}
		updates = append(updates, change)
		result.Added += count.Added
		result.Updated += count.Updated
	}
	for _, key := range desired.Remove {
		if _, exists := state.byKey[key]; exists {
			removals = append(removals, Change{Peer: Peer{PublicKey: key}, Remove: true})
			result.Removed++
		}
	}
	if result.Added != 0 && len(state.byKey)+int(result.Added)-int(result.Removed) > MaxPeers {
		return nil, nil, Result{}, ErrCapacity
	}
	return updates, removals, result, nil
}

func (state *State) plan(desired Peer) (Change, Result) {
	before, exists := state.byKey[desired.PublicKey]
	if !exists {
		return peerChange(nil, desired), Result{Added: 1}
	}
	changeIPs := !samePrefixes(before.prefixes, desired.AllowedIPs)
	changePSK := before.pskHash != presharedKeyHash(desired.PresharedKey)
	if changeIPs || changePSK {
		return Change{Peer: desired, ReplaceAllowedIPs: changeIPs, SetPresharedKey: changePSK}, Result{Updated: 1}
	}
	return Change{}, Result{}
}

func (state *State) CommitPeer(change Change) {
	state.Delete(change.PublicKey)
	state.put(change.Peer)
}

func (state *State) CommitDelta(updates, removals []Change) {
	// Release the old prefixes first so a transfer between updated peers does
	// not depend on the order of the updates.
	for _, change := range removals {
		state.Delete(change.PublicKey)
	}
	for _, change := range updates {
		state.Delete(change.PublicKey)
	}
	for _, change := range updates {
		state.put(change.Peer)
	}
}

func (state *State) Delete(key [32]byte) {
	state.lastSync = [32]byte{}
	for _, prefix := range state.byKey[key].prefixes {
		delete(state.owners, prefix)
	}
	delete(state.byKey, key)
}

func (state *State) put(peer Peer) {
	state.byKey[peer.PublicKey] = confirmedPeer{
		prefixes: slices.Clone(peer.AllowedIPs), pskHash: presharedKeyHash(peer.PresharedKey),
	}
	for _, prefix := range peer.AllowedIPs {
		state.owners[prefix] = peer.PublicKey
	}
}

func presharedKeyHash(key [32]byte) [32]byte {
	if key == [32]byte{} {
		return key
	}
	return sha256.Sum256(key[:])
}

func deltaKeys(delta Delta) map[[32]byte]bool {
	keys := make(map[[32]byte]bool, len(delta.Upsert)+len(delta.Remove))
	for _, peer := range delta.Upsert {
		keys[peer.PublicKey] = true
	}
	for _, key := range delta.Remove {
		keys[key] = true
	}
	return keys
}
