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
		key, err := Key(item.PublicKey)
		if err != nil || !validPresharedKey(item.PresharedKey) {
			return nil, ErrInvalid
		}
		peer := &desired[i]
		peer.PublicKey = key
		if keys[peer.PublicKey] {
			return nil, ErrInvalid
		}
		keys[peer.PublicKey] = true
		copy(peer.PresharedKey[:], item.PresharedKey)
		start := len(addresses)
		for _, raw := range item.AllowedIPs {
			prefix, err := parsePrefix(raw)
			if err != nil {
				return nil, ErrInvalid
			}
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
	if samePeerOrder(current, desired) {
		return nil, nil, result
	}
	byKey := make(map[[32]byte]int, len(current))
	for i := range current {
		byKey[current[i].PublicKey] = i
	}
	for i := range desired {
		next := &desired[i]
		index, exists := byKey[next.PublicKey]
		if !exists {
			result.Added++
			updates = append(updates, peerChange(nil, *next))
			continue
		}
		delete(byKey, next.PublicKey)
		change := peerChange(&current[index], *next)
		if change.ReplaceAllowedIPs || change.SetPresharedKey {
			result.Updated++
			updates = append(updates, change)
		}
	}
	for key := range byKey {
		result.Removed++
		removals = append(removals, Change{Peer: Peer{PublicKey: key}, Remove: true})
	}
	return updates, removals, result
}

func samePeerOrder(current, desired []Peer) bool {
	return slices.EqualFunc(current, desired, func(before, next Peer) bool {
		return before.PublicKey == next.PublicKey && before.PresharedKey == next.PresharedKey &&
			samePrefixes(before.AllowedIPs, next.AllowedIPs)
	})
}

func validPresharedKey(key []byte) bool { return len(key) == 0 || len(key) == 32 }

func parsePrefix(raw string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(raw)
	if err != nil || prefix.Addr().Is4In6() {
		return netip.Prefix{}, ErrInvalid
	}
	return prefix.Masked(), nil
}

func peerChange(before *Peer, next Peer) Change {
	if before == nil {
		return Change{Peer: next, ReplaceAllowedIPs: true, SetPresharedKey: next.PresharedKey != [32]byte{}}
	}
	return Change{Peer: next, ReplaceAllowedIPs: !samePrefixes(before.AllowedIPs, next.AllowedIPs), SetPresharedKey: before.PresharedKey != next.PresharedKey}
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
