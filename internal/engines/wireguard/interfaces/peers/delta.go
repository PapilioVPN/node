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
