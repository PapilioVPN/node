package peers

import (
	"errors"
	"net/netip"
	"slices"
)

var (
	ErrPrefixConflict = errors.New("wireguard prefix belongs to a peer outside the requested changes")
	ErrCapacity       = errors.New("wireguard interface would exceed 1000 managed peers")
)

type DeltaInput struct {
	Upsert []Input  `json:"upsert"`
	Remove [][]byte `json:"remove"`
}

type Delta struct {
	Upsert []Peer
	Remove [][32]byte
}

func Key(input []byte) ([32]byte, error) {
	if len(input) != 32 || [32]byte(input) == [32]byte{} {
		return [32]byte{}, ErrInvalid
	}
	return [32]byte(input), nil
}

func PreparePeer(input Input) (Peer, error) {
	key, err := Key(input.PublicKey)
	if err != nil || len(input.AllowedIPs) > MaxAllowedIPs || !validPresharedKey(input.PresharedKey) {
		return Peer{}, ErrInvalid
	}
	desired := Peer{PublicKey: key, AllowedIPs: make([]netip.Prefix, 0, len(input.AllowedIPs))}
	copy(desired.PresharedKey[:], input.PresharedKey)
	for _, raw := range input.AllowedIPs {
		prefix, err := parsePrefix(raw)
		if err != nil || slices.Contains(desired.AllowedIPs, prefix) {
			return Peer{}, ErrInvalid
		}
		desired.AllowedIPs = append(desired.AllowedIPs, prefix)
	}
	return desired, nil
}

func PrepareDelta(input DeltaInput) (Delta, error) {
	count := len(input.Upsert) + len(input.Remove)
	if count == 0 {
		return Delta{}, ErrInvalid
	}
	if count > MaxPeers {
		return Delta{}, ErrTooMany
	}
	var delta Delta
	if len(input.Upsert) != 0 {
		upserts, err := Prepare(input.Upsert)
		if err != nil {
			return Delta{}, err
		}
		delta.Upsert = upserts
	}
	if len(input.Remove) == 0 {
		return delta, nil
	}
	keys := make(map[[32]byte]bool, count)
	for _, peer := range delta.Upsert {
		keys[peer.PublicKey] = true
	}
	delta.Remove = make([][32]byte, 0, len(input.Remove))
	for _, raw := range input.Remove {
		key, err := Key(raw)
		if err != nil || keys[key] {
			return Delta{}, ErrInvalid
		}
		keys[key] = true
		delta.Remove = append(delta.Remove, key)
	}
	return delta, nil
}

func PlanPeer(current []Peer, desired Peer) (Change, Result, error) {
	var before *Peer
	for i := range current {
		peer := &current[i]
		if peer.PublicKey == desired.PublicKey {
			before = peer
			break
		}
	}
	change := peerChange(before, desired)
	if change.ReplaceAllowedIPs && len(desired.AllowedIPs) != 0 {
		for i := range current {
			peer := &current[i]
			if peer == before {
				continue
			}
			for _, prefix := range desired.AllowedIPs {
				if slices.Contains(peer.AllowedIPs, prefix) {
					return Change{}, Result{}, ErrPrefixConflict
				}
			}
		}
	}
	if before == nil {
		if len(current) >= MaxPeers {
			return Change{}, Result{}, ErrCapacity
		}
		return change, Result{Added: 1}, nil
	}
	if change.ReplaceAllowedIPs || change.SetPresharedKey {
		return change, Result{Updated: 1}, nil
	}
	return Change{}, Result{}, nil
}

func PlanDelta(current []Peer, desired Delta) (updates, removals []Change, result Result, err error) {
	if len(desired.Remove) == 0 && samePeerOrder(current, desired.Upsert) {
		return nil, nil, result, nil
	}
	touched := make(map[[32]byte]bool, len(desired.Upsert)+len(desired.Remove))
	claims := make(map[netip.Prefix]bool)
	for _, peer := range desired.Upsert {
		touched[peer.PublicKey] = true
		for _, prefix := range peer.AllowedIPs {
			claims[prefix] = true
		}
	}
	for _, key := range desired.Remove {
		touched[key] = true
	}
	selected := make([]Peer, 0, len(touched))
	for i := range current {
		peer := &current[i]
		if touched[peer.PublicKey] {
			selected = append(selected, *peer)
			continue
		}
		for _, prefix := range peer.AllowedIPs {
			if claims[prefix] {
				return nil, nil, Result{}, ErrPrefixConflict
			}
		}
	}
	updates, removals, result = Diff(selected, desired.Upsert)
	if result.Added > 0 && len(current)+int(result.Added)-int(result.Removed) > MaxPeers {
		return nil, nil, Result{}, ErrCapacity
	}
	return updates, removals, result, nil
}
