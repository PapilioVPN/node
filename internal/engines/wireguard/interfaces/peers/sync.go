// Package peers owns the desired WireGuard peer set and its reconciliation.
package peers

import (
	"errors"
	"net/netip"
	"slices"
)

const (
	MaxPeers      = 1000
	MaxAllowedIPs = 16
)

var (
	ErrInvalid     = errors.New("invalid wireguard peer set")
	ErrTooMany     = errors.New("wireguard peer set exceeds 1000 peers")
	ErrUnconfirmed = errors.New("wireguard peer sync outcome could not be confirmed")
)

type Input struct {
	PublicKey    []byte   `json:"public_key"`
	PresharedKey []byte   `json:"preshared_key"`
	AllowedIPs   []string `json:"allowed_ips"`
}

type Peer struct {
	PublicKey    [32]byte
	PresharedKey [32]byte
	AllowedIPs   []netip.Prefix
}

type Result struct {
	Added   uint32 `json:"added"`
	Updated uint32 `json:"updated"`
	Removed uint32 `json:"removed"`
}

type Change struct {
	Peer
	Remove            bool
	ReplaceAllowedIPs bool
	SetPresharedKey   bool
}

func Prepare(input []Input) ([]Peer, error) {
	if input == nil {
		return nil, ErrInvalid
	}
	if len(input) > MaxPeers {
		return nil, ErrTooMany
	}
	prefixCount := 0
	for _, item := range input {
		if len(item.AllowedIPs) > MaxAllowedIPs {
			return nil, ErrInvalid
		}
		prefixCount += len(item.AllowedIPs)
	}
	desired := make([]Peer, len(input))
	keys := make(map[[32]byte]bool, len(input))
	prefixes := make(map[netip.Prefix]bool, prefixCount)
	addresses := make([]netip.Prefix, 0, prefixCount)
	for i, item := range input {
		if len(item.PublicKey) != 32 || [32]byte(item.PublicKey) == [32]byte{} ||
			len(item.PresharedKey) != 0 && len(item.PresharedKey) != 32 {
			return nil, ErrInvalid
		}
		peer := &desired[i]
		peer.PublicKey = [32]byte(item.PublicKey)
		if keys[peer.PublicKey] {
			return nil, ErrInvalid
		}
		keys[peer.PublicKey] = true
		copy(peer.PresharedKey[:], item.PresharedKey)
		start := len(addresses)
		for _, raw := range item.AllowedIPs {
			prefix, err := netip.ParsePrefix(raw)
			if err != nil || prefix.Addr().Is4In6() {
				return nil, ErrInvalid
			}
			prefix = prefix.Masked()
			if prefixes[prefix] {
				return nil, ErrInvalid
			}
			prefixes[prefix] = true
			addresses = append(addresses, prefix)
		}
		peer.AllowedIPs = addresses[start:len(addresses):len(addresses)]
	}
	return desired, nil
}

func Diff(current, desired []Peer) (updates, removals []Change, result Result) {
	byKey := make(map[[32]byte]int, len(current))
	for i := range current {
		byKey[current[i].PublicKey] = i
	}
	for i := range desired {
		next := &desired[i]
		index, exists := byKey[next.PublicKey]
		if !exists {
			result.Added++
			updates = append(updates, Change{Peer: *next, ReplaceAllowedIPs: true, SetPresharedKey: next.PresharedKey != [32]byte{}})
			continue
		}
		delete(byKey, next.PublicKey)
		before := &current[index]
		changeIPs := !samePrefixes(before.AllowedIPs, next.AllowedIPs)
		changeKey := before.PresharedKey != next.PresharedKey
		if changeIPs || changeKey {
			result.Updated++
			updates = append(updates, Change{Peer: *next, ReplaceAllowedIPs: changeIPs, SetPresharedKey: changeKey})
		}
	}
	for key := range byKey {
		result.Removed++
		removals = append(removals, Change{Peer: Peer{PublicKey: key}, Remove: true})
	}
	return updates, removals, result
}

func samePrefixes(a, b []netip.Prefix) bool {
	if len(a) != len(b) {
		return false
	}
	if slices.Equal(a, b) {
		return true
	}
	for _, prefix := range a {
		if !slices.Contains(b, prefix) {
			return false
		}
	}
	return true
}
