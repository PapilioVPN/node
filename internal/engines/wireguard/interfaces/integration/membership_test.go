package integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"os/exec"
	"strconv"
	"testing"

	interfacesv1 "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/grpc/proto/v1"
)

type peerMembers struct {
	Peers []struct {
		PublicKey  []byte         `json:"public_key"`
		AllowedIPs []netip.Prefix `json:"allowed_ips"`
	} `json:"peers"`
}

func readPeerMembers(t testing.TB, f fixture) (peerMembers, []byte) {
	t.Helper()
	response, err := f.http.Get(f.address + interfaceURL + "/peers")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("ListPeers returned %s", response.Status)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var members peerMembers
	if err := json.Unmarshal(body, &members); err != nil {
		t.Fatal(err)
	}
	return members, body
}

func assertPeerMembership(t *testing.T, f fixture, key [32]byte) {
	t.Helper()
	members, body := readPeerMembers(t, f)
	want := netip.MustParsePrefix("192.0.2.2/32")
	if len(members.Peers) != 1 || !bytes.Equal(members.Peers[0].PublicKey, key[:]) ||
		len(members.Peers[0].AllowedIPs) != 1 || members.Peers[0].AllowedIPs[0] != want ||
		bytes.Contains(body, []byte("preshared_key")) {
		t.Fatalf("unexpected HTTP peer membership: %s", body)
	}
	reply, err := f.grpcClient(t).ListPeers(t.Context(), &interfacesv1.WireGuardInterfaceRequest{Name: interfaceName})
	if err != nil {
		t.Fatal(err)
	}
	got := reply.GetPeers()
	if len(got) != 1 || !bytes.Equal(got[0].GetPublicKey(), key[:]) ||
		len(got[0].GetAllowedIps()) != 1 || got[0].GetAllowedIps()[0] != want.String() {
		t.Fatalf("unexpected gRPC peer membership: %v", reply)
	}
}

func BenchmarkPeerMembership(b *testing.B) {
	for _, count := range []int{1, 1000} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			f := newFixture(b)
			setPeers(b, count)
			b.Run("HTTP", func(b *testing.B) {
				for b.Loop() {
					members, _ := readPeerMembers(b, f)
					if len(members.Peers) != count {
						b.Fatalf("HTTP returned %d peers", len(members.Peers))
					}
				}
			})
			b.Run("gRPC", func(b *testing.B) {
				client := f.grpcClient(b)
				request := &interfacesv1.WireGuardInterfaceRequest{Name: interfaceName}
				for b.Loop() {
					reply, err := client.ListPeers(b.Context(), request)
					if err != nil || len(reply.GetPeers()) != count {
						b.Fatalf("gRPC returned %d peers: %v", len(reply.GetPeers()), err)
					}
				}
			})
			b.Run("wg", func(b *testing.B) {
				for b.Loop() {
					body, err := exec.CommandContext(b.Context(), "wg", "show", interfaceName, "allowed-ips").Output()
					if err != nil || bytes.Count(body, []byte("\n")) != count {
						b.Fatalf("wg show: %v", err)
					}
				}
			})
		})
	}
}
