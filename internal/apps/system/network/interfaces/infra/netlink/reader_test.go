//go:build linux

package netlink

import (
	"context"
	"errors"
	"net"
	"testing"

	native "github.com/vishvananda/netlink"
)

func TestInterfaceInventoryUsesTwoBulkCallsAndPreservesUnmatchedAddresses(t *testing.T) {
	for _, size := range []int{0, 1, 64, 4096} {
		fixture := &sourceFixture{links: fixtureLinks(size)}
		fixture.addresses = []native.Addr{fixtureAddress(t, 999999, "192.0.2.99/24")}
		if size > 0 {
			fixture.addresses = append(fixture.addresses, fixtureAddress(t, 1, "192.0.2.4/24"), fixtureAddress(t, 1, "fe80::4/64"))
		}
		got, err := fixture.reader().List(context.Background())
		if err != nil || len(got.Interfaces) != size || fixture.linkCalls != 1 || fixture.addressCalls != 1 || fixture.closes != 1 || len(fixture.timeouts) != 2 || len(got.UnmatchedAddresses) != 1 || got.UnmatchedAddresses[0].InterfaceIndex != 999999 {
			t.Fatalf("inventory size %d: %+v %v calls=%d/%d closed=%d", size, got, err, fixture.linkCalls, fixture.addressCalls, fixture.closes)
		}
		if size > 0 && (len(got.Interfaces[0].Addresses) != 2 || got.Interfaces[0].Addresses[0].LocalPrefix != "192.0.2.4/24" || got.Interfaces[0].Addresses[1].Family != "ipv6") {
			t.Fatalf("lost addresses: %+v", got.Interfaces[0])
		}
	}
}

func TestInterfaceCountersDoNotReadAddressesAndRetainMissingStatistics(t *testing.T) {
	for _, size := range []int{1, 64, 4096} {
		fixture := &sourceFixture{links: fixtureLinks(size)}
		fixture.links[0].Attrs().Statistics = &native.LinkStatistics{RxBytes: 9007199254740993, TxBytes: 1<<64 - 1, RxPackets: 2}
		got, err := fixture.reader().Counters(context.Background())
		if err != nil || len(got) != size || fixture.linkCalls != 1 || fixture.addressCalls != 0 || fixture.closes != 1 || got[0].Counters.ReceiveBytes != 9007199254740993 || got[0].Counters.TransmitBytes != 1<<64-1 || got[0].Counters.ReceivePackets != 2 {
			t.Fatalf("counter read: %+v %v", got, err)
		}
		if size > 1 && got[1].Counters != nil {
			t.Fatal("fabricated missing counters")
		}
	}
}

func TestInterfaceReadFailuresCloseResourcesWithoutPartialResults(t *testing.T) {
	cause := errors.New("native failure")
	for _, fixture := range []*sourceFixture{{links: fixtureLinks(1), linkError: cause}, {links: fixtureLinks(1), addressError: cause}, {timeoutError: cause}, {links: fixtureLinks(1), linkError: native.ErrDumpInterrupted}} {
		got, err := fixture.reader().List(context.Background())
		if err == nil || got.Interfaces != nil || fixture.closes != 1 {
			t.Fatalf("partial/unclosed inventory: %+v %v %+v", got, err, fixture)
		}
		if fixture.linkError == native.ErrDumpInterrupted && !errors.Is(err, native.ErrDumpInterrupted) {
			t.Fatal("lost dump interruption")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	fixture := &sourceFixture{links: fixtureLinks(1), afterLinks: cancel}
	got, err := fixture.reader().List(ctx)
	if !errors.Is(err, context.Canceled) || got.Interfaces != nil || fixture.addressCalls != 0 || fixture.closes != 1 {
		t.Fatalf("canceled inventory: %+v %v %+v", got, err, fixture)
	}
	fixture = &sourceFixture{}
	if _, err := fixture.reader().List(ctx); !errors.Is(err, context.Canceled) || fixture.linkCalls != 0 || fixture.closes != 0 {
		t.Fatal("canceled read opened a resource")
	}
	duplicate := fixtureLinks(2)
	duplicate[1].Attrs().Index = 1
	for _, links := range [][]native.Link{duplicate, {nil}, make([]native.Link, maxInterfaces+1)} {
		fixture := &sourceFixture{links: links}
		if got, err := fixture.reader().List(context.Background()); err == nil || got.Interfaces != nil || fixture.closes != 1 {
			t.Fatalf("bad links accepted: %+v %v", got, err)
		}
	}
	fixture = &sourceFixture{addresses: make([]native.Addr, maxAddresses+1)}
	if _, err := fixture.reader().List(context.Background()); err == nil || fixture.closes != 1 {
		t.Fatal("address bound ignored")
	}
}

func TestAddressProjectionRetainsPeerLifetimeFlagsAndIPv6Family(t *testing.T) {
	value := fixtureAddress(t, 42, "192.0.2.4/32")
	peer := fixtureAddress(t, 42, "198.51.100.5/24")
	value.Peer = peer.IPNet
	value.Broadcast = net.ParseIP("198.51.100.255")
	value.Flags = 0x80000000
	value.Scope = 253
	value.Label = "point-to-point"
	got, err := addressRecord(value)
	if err != nil || got.InterfaceIndex != 42 || got.PeerPrefix == nil || *got.PeerPrefix != "198.51.100.5/24" || got.Broadcast == nil || *got.Broadcast != "198.51.100.255" || got.ValidLifetimeSeconds != 1<<32-1 || got.Flags != 0x80000000 || got.Scope != 253 {
		t.Fatalf("lost native address facts: %+v %v", got, err)
	}
	mapped := native.Addr{LinkIndex: 1, IPNet: &net.IPNet{IP: net.ParseIP("::ffff:192.0.2.4"), Mask: net.CIDRMask(128, 128)}}
	got, err = addressRecord(mapped)
	if err != nil || got.Family != "ipv6" || got.LocalPrefix != "::ffff:192.0.2.4/128" {
		t.Fatalf("lost mapped IPv6 identity: %+v %v", got, err)
	}
	for _, bad := range []native.Addr{{LinkIndex: 1}, {LinkIndex: 0, IPNet: value.IPNet}, {LinkIndex: 1, IPNet: value.IPNet, ValidLft: -1}, {LinkIndex: 1, IPNet: value.IPNet, Label: string([]byte{255})}} {
		if _, err := addressRecord(bad); err == nil {
			t.Fatal("bad address accepted")
		}
	}
}
