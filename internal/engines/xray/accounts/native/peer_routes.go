package native

import (
	"crypto/ecdh"
	"encoding/hex"
	"net/netip"
	"slices"

	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"google.golang.org/protobuf/proto"
)

func devicePublicKey(secret string) string {
	raw, err := hex.DecodeString(secret)
	if err != nil {
		return ""
	}
	key, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return ""
	}
	return hex.EncodeToString(key.PublicKey().Bytes())
}

type peerRoute struct {
	prefix netip.Prefix
	name   string
	new    bool
}

func (e *editor) checkPeerRoutes(removals []string, additions []membership.PreparedAccount) error {
	if e.entry.layout.protocol != membership.WireGuard || len(additions) == 0 {
		return nil
	}
	removed := make(map[string]bool, len(removals))
	for _, name := range removals {
		removed[name] = true
	}
	var routes []peerRoute
	for name, record := range e.entry.records {
		if !removed[name] {
			if err := appendPeerRoutes(&routes, name, record.Account.Value, false); err != nil {
				return err
			}
		}
	}
	for _, record := range e.entry.anonymousPeers {
		if err := appendPeerRoutes(&routes, "", record.Account.Value, false); err != nil {
			return err
		}
	}
	for _, account := range additions {
		if err := appendPeerRoutes(&routes, account.Name, account.Value, true); err != nil {
			return err
		}
	}
	return checkRouteOwners(routes)
}

func appendPeerRoutes(routes *[]peerRoute, name string, wire []byte, proposed bool) error {
	var peer WireGuardAccount
	if proto.Unmarshal(wire, &peer) != nil {
		return membership.ErrAmbiguous
	}
	for _, text := range peer.AllowedIps {
		prefix, err := netip.ParsePrefix(text)
		if err != nil {
			return membership.ErrAmbiguous
		}
		*routes = append(*routes, peerRoute{prefix.Masked(), name, proposed})
	}
	return nil
}

// A broader prefix sorts before its subnets. The stack contains exactly those
// earlier routes covering the current prefix. Existing overlaps do not prevent
// removing accounts or editing an unrelated peer.
func checkRouteOwners(routes []peerRoute) error {
	slices.SortFunc(routes, func(a, b peerRoute) int {
		if order := a.prefix.Addr().Compare(b.prefix.Addr()); order != 0 {
			return order
		}
		return a.prefix.Bits() - b.prefix.Bits()
	})
	active := make([]peerRoute, 0)
	owners, proposed := make(map[string]int), make(map[string]int)
	proposedCount := 0
	for _, route := range routes {
		for len(active) > 0 && !active[len(active)-1].prefix.Contains(route.prefix.Addr()) {
			previous := active[len(active)-1]
			active = active[:len(active)-1]
			owners[previous.name]--
			if previous.new {
				proposed[previous.name]--
				proposedCount--
			}
		}
		if (route.new && len(active) > owners[route.name]) || proposedCount > proposed[route.name] {
			return membership.ErrExists
		}
		active = append(active, route)
		owners[route.name]++
		if route.new {
			proposed[route.name]++
			proposedCount++
		}
	}
	return nil
}
