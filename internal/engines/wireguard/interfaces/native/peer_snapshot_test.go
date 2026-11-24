package native

import (
	"crypto/sha256"
	"errors"
	"net/netip"
	"reflect"
	"testing"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
	gennetlink "github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
)

func TestPeerSnapshotJoinsMultipartPrefixLists(t *testing.T) {
	key := sha256.Sum256([]byte("test peer"))
	psk := sha256.Sum256([]byte("fixture-only PSK"))
	prefixes := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("fd00::1/128")}
	var snapshot peerSnapshot
	for index, prefix := range prefixes {
		change := peers.Change{
			Peer:              peers.Peer{PublicKey: key, PresharedKey: psk, AllowedIPs: []netip.Prefix{prefix}},
			ReplaceAllowedIPs: true,
			SetPresharedKey:   index == 0,
		}
		message := peerSnapshotFragment(t, change)
		if err := snapshot.decode(message); err != nil {
			t.Fatal(err)
		}
		clear(message)
	}
	expected := []peers.Peer{{PublicKey: key, PresharedKey: psk, AllowedIPs: prefixes}}
	if !reflect.DeepEqual(snapshot.peers, expected) {
		t.Fatal("multipart decode did not retain the peer and both prefixes")
	}
}

func TestPeerSnapshotRejectsTruncatedAttributes(t *testing.T) {
	message := peerSnapshotFragment(t, peers.Change{Peer: peers.Peer{PublicKey: [32]byte{1}}})
	for length := 1; length < len(message); length++ {
		var snapshot peerSnapshot
		if err := snapshot.decode(message[:length]); !errors.Is(err, errPeerSnapshot) {
			t.Fatalf("truncated message at %d bytes returned %v", length, err)
		}
	}
}

func peerSnapshotFragment(t *testing.T, change peers.Change) []byte {
	t.Helper()
	encoder := gennetlink.NewAttributeEncoder()
	encoder.Nested(unix.WGDEVICE_A_PEERS, func(list *gennetlink.AttributeEncoder) error {
		list.Nested(0, func(peer *gennetlink.AttributeEncoder) error {
			encodePeerChange(peer, &change)
			return nil
		})
		return nil
	})
	message, err := encoder.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return message
}
