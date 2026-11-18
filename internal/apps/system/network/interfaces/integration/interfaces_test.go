//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"

	interfacesv1 "github.com/PapilioVPN/node/internal/apps/system/network/interfaces/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats.go"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

type addressObservation struct {
	Index       uint32  `json:"interface_index"`
	Family      string  `json:"family"`
	LocalPrefix string  `json:"local_prefix"`
	PeerPrefix  *string `json:"peer_prefix"`
	Valid       uint32  `json:"valid_lifetime_seconds"`
}
type interfaceObservation struct {
	Index           uint32               `json:"index"`
	Name            string               `json:"name"`
	MTU             uint32               `json:"mtu"`
	HardwareAddress string               `json:"hardware_address"`
	Flags           uint32               `json:"flags"`
	Addresses       []addressObservation `json:"addresses"`
}
type inventoryObservation struct {
	Interfaces []interfaceObservation `json:"interfaces"`
	Unmatched  []addressObservation   `json:"unmatched_addresses"`
	ObservedAt time.Time              `json:"observed_at"`
	DurationNS int64                  `json:"duration_ns,string"`
}
type counterValues struct {
	ReceiveBytes    uint64 `json:"receive_bytes,string"`
	TransmitBytes   uint64 `json:"transmit_bytes,string"`
	ReceivePackets  uint64 `json:"receive_packets,string"`
	TransmitPackets uint64 `json:"transmit_packets,string"`
	ReceiveErrors   uint64 `json:"receive_errors,string"`
	TransmitErrors  uint64 `json:"transmit_errors,string"`
	ReceiveDropped  uint64 `json:"receive_dropped,string"`
	ReceiveMissed   uint64 `json:"receive_missed_errors,string"`
	TransmitDropped uint64 `json:"transmit_dropped,string"`
}
type counterObservation struct {
	Interfaces []struct {
		Index    uint32         `json:"index"`
		Name     string         `json:"name"`
		Counters *counterValues `json:"counters"`
	} `json:"interfaces"`
	ObservedAt time.Time `json:"observed_at"`
	DurationNS int64     `json:"duration_ns,string"`
}

func TestNetworkInterfacesOverEachIndependentlyEnabledTransport(t *testing.T) {
	pki := testutil.NewPKI(t)
	all := nodeConfig(t, pki)
	for _, transport := range []string{"http", "grpc", "nats", "all"} {
		t.Run(transport, func(t *testing.T) {
			config := all
			if transport != "all" && transport != "http" {
				config.Communications.HTTP = nil
			}
			if transport != "all" && transport != "grpc" {
				config.Communications.GRPC = nil
			}
			if transport != "all" && transport != "nats" {
				config.Communications.NATS = nil
				config.Communications.Requests.NATS = nil
			}
			if err := config.Validate(); err != nil {
				t.Fatal(err)
			}
			assertInterfaceReads(t, startConfiguredNode(t, pki, config))
		})
	}
}

func assertInterfaceReads(t *testing.T, client clients) {
	t.Helper()
	checkInventory := func(body []byte, started time.Time) {
		t.Helper()
		var got inventoryObservation
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("inventory JSON: %s %v", body, err)
		}
		assertInventory(t, got, started)
	}
	checkCounters := func(body []byte, before map[string][]uint64, started time.Time) {
		t.Helper()
		var got counterObservation
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("counter JSON: %s %v", body, err)
		}
		assertCounters(t, got, before, hostCounters(t), started)
	}
	if client.HTTP != nil {
		started := time.Now()
		response, err := client.HTTP.Get(client.HTTPAddress)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("inventory HTTP: %s %v", body, err)
		}
		checkInventory(body, started)
		before, started := hostCounters(t), time.Now()
		response, err = client.HTTP.Get(client.HTTPAddress + "/counters")
		if err != nil {
			t.Fatal(err)
		}
		body, err = io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("counter HTTP: %s %v", body, err)
		}
		checkCounters(body, before, started)
	}
	if client.GRPC != nil {
		api := interfacesv1.NewInterfacesServiceClient(client.GRPC)
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		result, err := api.ListInterfaces(ctx, &interfacesv1.ListInterfacesRequest{})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		body, err := protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		checkInventory(body, started)
		before, started := hostCounters(t), time.Now()
		ctx, cancel = context.WithTimeout(context.Background(), time.Second)
		counters, err := api.GetInterfaceCounters(ctx, &interfacesv1.GetInterfaceCountersRequest{})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		body, err = protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}.Marshal(counters)
		if err != nil {
			t.Fatal(err)
		}
		checkCounters(body, before, started)
		ctx, cancel = context.WithCancel(context.Background())
		cancel()
		if _, err := api.ListInterfaces(ctx, &interfacesv1.ListInterfacesRequest{}); status.Code(err) != codes.Canceled {
			t.Fatal(err)
		}
		if _, err := api.GetInterfaceCounters(ctx, &interfacesv1.GetInterfaceCountersRequest{}); status.Code(err) != codes.Canceled {
			t.Fatal(err)
		}
	}
	if client.NATS != nil {
		started := time.Now()
		response, err := client.NATS.Request(subject, []byte("{}"), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		checkInventory(response.Data, started)
		before, started := hostCounters(t), time.Now()
		response, err = client.NATS.Request(counterSubject, []byte("{}"), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		checkCounters(response.Data, before, started)
		for _, subject := range []string{subject, counterSubject} {
			expired := nats.NewMsg(subject)
			expired.Data = []byte("{}")
			expired.Header.Set("Papilio-Deadline", time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano))
			response, err := client.NATS.RequestMsg(expired, time.Second)
			if err != nil || string(response.Data) != `{"error":"deadline_exceeded"}` {
				t.Fatalf("expired interfaces: %v %v", response, err)
			}
		}
	}
}

const counterSubject = "fixture.node-a.system.network.interfaces.counters.v1.get"

func assertInventory(t *testing.T, got inventoryObservation, started time.Time) {
	t.Helper()
	reference := hostInterfaces(t)
	if len(got.Interfaces) != len(reference) || len(got.Unmatched) != 0 {
		t.Fatalf("native inventory differs: got %d, want %d; unmatched=%d", len(got.Interfaces), len(reference), len(got.Unmatched))
	}
	seen := map[uint32]bool{}
	for _, row := range got.Interfaces {
		source, ok := reference[row.Index]
		if !ok || seen[row.Index] || row.Name != source.Name || row.MTU != uint32(source.MTU) || row.HardwareAddress != source.HardwareAddr.String() {
			t.Fatalf("lost native interface identity: %d", row.Index)
		}
		seen[row.Index] = true
		for _, flag := range []struct {
			native   uint32
			standard net.Flags
		}{{unix.IFF_UP, net.FlagUp}, {unix.IFF_LOOPBACK, net.FlagLoopback}, {unix.IFF_BROADCAST, net.FlagBroadcast}, {unix.IFF_POINTOPOINT, net.FlagPointToPoint}, {unix.IFF_MULTICAST, net.FlagMulticast}} {
			if (row.Flags&flag.native != 0) != (source.Flags&flag.standard != 0) {
				t.Fatalf("interface %d flags differ", row.Index)
			}
		}
		addresses, err := source.Addrs()
		if err != nil {
			t.Fatal(err)
		}
		// The independent standard-library view has no separate peer prefix. Compare
		// local identities/families; controlled tests verify exact P2P prefix semantics.
		expected := map[string]bool{}
		for _, address := range addresses {
			ip, ok := address.(*net.IPNet)
			if !ok {
				t.Fatal("unexpected standard address")
			}
			value, ok := netip.AddrFromSlice(ip.IP)
			if !ok {
				t.Fatal("invalid standard address")
			}
			_, bits := ip.Mask.Size()
			family := "ipv6"
			if bits == 32 {
				family = "ipv4"
			}
			expected[family+":"+value.Unmap().String()] = true
		}
		if len(row.Addresses) != len(addresses) {
			t.Fatalf("interface %d address count differs", row.Index)
		}
		for _, address := range row.Addresses {
			prefix, err := netip.ParsePrefix(address.LocalPrefix)
			if err != nil || address.Index != row.Index || !expected[address.Family+":"+prefix.Addr().Unmap().String()] {
				t.Fatalf("interface %d local address differs", row.Index)
			}
		}
	}
	assertTiming(t, got.ObservedAt, got.DurationNS, started)
}

func assertCounters(t *testing.T, got counterObservation, before, after map[string][]uint64, started time.Time) {
	t.Helper()
	identities := hostInterfaces(t)
	seen := map[uint32]bool{}
	if len(got.Interfaces) != len(identities) {
		t.Fatal("counter inventory incomplete")
	}
	for _, row := range got.Interfaces {
		link, ok := identities[row.Index]
		if !ok || link.Name != row.Name || seen[row.Index] || row.Counters == nil {
			t.Fatalf("lost counter identity/presence: %d", row.Index)
		}
		seen[row.Index] = true
		low, ok := before[row.Name]
		high, present := after[row.Name]
		if !ok || !present {
			t.Fatal("reference interface changed during observation")
		}
		c := row.Counters
		for _, field := range []struct {
			value  uint64
			column int
		}{{c.ReceiveBytes, 0}, {c.ReceivePackets, 1}, {c.ReceiveErrors, 2}, {c.ReceiveDropped + c.ReceiveMissed, 3}, {c.TransmitBytes, 8}, {c.TransmitPackets, 9}, {c.TransmitErrors, 10}, {c.TransmitDropped, 11}} {
			if field.value < low[field.column] || field.value > high[field.column] {
				t.Fatalf("interface %d counter %d outside independent source readings", row.Index, field.column)
			}
		}
	}
	assertTiming(t, got.ObservedAt, got.DurationNS, started)
}

func assertTiming(t *testing.T, observed time.Time, duration int64, started time.Time) {
	t.Helper()
	if observed.Before(started) || observed.After(time.Now()) || duration < 0 || duration > int64(time.Since(started)) {
		t.Fatal("invalid observation timing")
	}
}
