package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	interfacesv1 "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/grpc/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type peerSyncReply struct {
	Added   int `json:"added"`
	Updated int `json:"updated"`
	Removed int `json:"removed"`
}

func assertPeerSync(t *testing.T, f fixture, client interfacesv1.WireGuardInterfacesServiceClient, active string) {
	t.Helper()
	addedKey := sha256.Sum256([]byte("additional wireguard peer"))
	added := base64.StdEncoding.EncodeToString(addedKey[:])
	initial := wgOutput(t, "latest-handshakes")
	if strings.HasSuffix(strings.TrimSpace(initial), "\t0") {
		t.Fatal("fixture peer has no confirmed handshake")
	}
	activePeer := fmt.Sprintf(`{"public_key":%q,"allowed_ips":["192.0.2.2/32"]}`, active)
	input := fmt.Appendf(nil, `{"peers":[%s,{"public_key":%q,"allowed_ips":["192.0.2.3/32"]}]}`, activePeer, added)
	if got := syncPeersHTTP(t, f, input, http.StatusOK); got != (peerSyncReply{Added: 1}) {
		t.Fatalf("add returned %+v", got)
	}
	if got := syncPeersHTTP(t, f, input, http.StatusOK); got != (peerSyncReply{}) {
		t.Fatalf("idempotent sync returned %+v", got)
	}
	legacy := bytes.Replace(input, []byte(`"peers"`), []byte(`"PEERS"`), 1)
	if got := syncPeersHTTP(t, f, legacy, http.StatusOK); got != (peerSyncReply{}) {
		t.Fatalf("replay with a case-insensitive field returned %+v", got)
	}
	syncPeersHTTP(t, f, append(bytes.Clone(input), []byte(` {}`)...), http.StatusBadRequest)
	syncPeersHTTP(t, f, []byte(`{"peers":[],"unexpected":true}`), http.StatusBadRequest)
	if err := exec.CommandContext(t.Context(), "wg", "set", interfaceName, "peer", added, "allowed-ips", "192.0.2.9/32").Run(); err != nil {
		t.Fatal(err)
	}
	if got := syncPeersHTTP(t, f, input, http.StatusOK); got != (peerSyncReply{}) {
		t.Fatalf("managed replay returned %+v", got)
	}
	if got := syncPeersHTTP(t, f, append([]byte(`{"reconcile":true,`), input[1:]...), http.StatusOK); got != (peerSyncReply{Updated: 1}) {
		t.Fatalf("explicit reconcile with kernel drift returned %+v", got)
	}
	changed := bytes.Replace(input, []byte("192.0.2.3/32"), []byte("192.0.2.4/32"), 1)
	if got := syncPeersHTTP(t, f, changed, http.StatusOK); got != (peerSyncReply{Updated: 1}) {
		t.Fatalf("update returned %+v", got)
	}
	if got := wgOutput(t, "allowed-ips"); !strings.Contains(got, added+"\t192.0.2.4/32") {
		t.Fatalf("changed peer missing expected prefix: %q", got)
	}
	activeKey, err := base64.StdEncoding.DecodeString(active)
	if err != nil {
		t.Fatal(err)
	}
	psk := sha256.Sum256([]byte("fixture-only preshared key"))
	rpc := syncPeersGRPC(t, client, []*interfacesv1.WireGuardPeer{
		{PublicKey: activeKey, AllowedIps: []string{"192.0.2.2/32"}},
		{PublicKey: addedKey[:], PresharedKey: psk[:], AllowedIps: []string{"192.0.2.4/32"}},
	})
	if rpc.Updated != 1 || rpc.Added != 0 || rpc.Removed != 0 ||
		!strings.Contains(wgOutput(t, "preshared-keys"), added+"\t"+base64.StdEncoding.EncodeToString(psk[:])) {
		t.Fatal("gRPC did not update the preshared key")
	}
	if got := syncPeersHTTP(t, f, changed, http.StatusOK); got != (peerSyncReply{Updated: 1}) {
		t.Fatalf("HTTP replay after gRPC updated the PSK returned %+v", got)
	}
	if !strings.Contains(wgOutput(t, "preshared-keys"), added+"\t(none)") {
		t.Fatal("HTTP replay did not clear the preshared key")
	}
	if got := syncPeersHTTP(t, f, fmt.Appendf(nil, `{"peers":[%s]}`, activePeer), http.StatusOK); got != (peerSyncReply{Removed: 1}) {
		t.Fatalf("remove returned %+v", got)
	}
	syncPeersHTTP(t, f, []byte(`{}`), http.StatusBadRequest)
	if got := wgOutput(t, "latest-handshakes"); got != initial {
		t.Fatalf("sync replaced the established peer: got %q, want %q", got, initial)
	}
}

func assertBulkPeerSync(t *testing.T, f fixture, client interfacesv1.WireGuardInterfacesServiceClient) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	_, err := client.SyncPeers(ctx, &interfacesv1.WireGuardSyncPeersRequest{Name: interfaceName})
	cancel()
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing desired list returned %v", err)
	}
	desired := make([]*interfacesv1.WireGuardPeer, 1000)
	for i := range desired {
		key := sha256.Sum256(fmt.Appendf(nil, "wireguard bulk fixture %d", i))
		prefixes := make([]string, 16)
		for j := range prefixes {
			prefixes[j] = fmt.Sprintf("fd00::%x:%x/128", i+1, j+1)
		}
		desired[i] = &interfacesv1.WireGuardPeer{PublicKey: key[:], AllowedIps: prefixes}
	}
	if got := syncPeersGRPC(t, client, desired); got.Added != 1000 || got.Removed != 1 {
		t.Fatalf("bulk sync returned %+v", got)
	}
	if got := syncPeersGRPC(t, client, desired); got.Added != 0 || got.Updated != 0 || got.Removed != 0 {
		t.Fatalf("bulk replay returned %+v", got)
	}
	firstKey := base64.StdEncoding.EncodeToString(desired[0].GetPublicKey())
	if err := exec.CommandContext(t.Context(), "wg", "set", interfaceName, "peer", firstKey, "allowed-ips", "fd00::ffff/128").Run(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(t.Context(), 3*time.Second)
	reply, err := client.SyncPeers(ctx, &interfacesv1.WireGuardSyncPeersRequest{Name: interfaceName,
		Desired: &interfacesv1.WireGuardPeerSet{Peers: desired}, Reconcile: true,
	})
	cancel()
	if err != nil || reply.GetUpdated() != 1 || reply.GetAdded() != 0 || reply.GetRemoved() != 0 {
		t.Fatalf("gRPC reconcile returned %v, %v", reply, err)
	}
	lines := strings.Split(strings.TrimSpace(wgOutput(t, "allowed-ips")), "\n")
	if len(lines) != 1000 {
		t.Fatalf("kernel returned %d peers", len(lines))
	}
	for _, line := range lines {
		if len(strings.Fields(line)) != 17 {
			t.Fatal("kernel peer has an incomplete prefix list")
		}
	}
	if got := syncPeersHTTP(t, f, []byte(`{"peers":[]}`), http.StatusOK); got.Removed != 1000 {
		t.Fatalf("explicit clear returned %+v", got)
	}
	if got := wgOutput(t, "peers"); got != "" {
		t.Fatal("explicit empty set left peers in the kernel")
	}
}

func syncPeersGRPC(t *testing.T, client interfacesv1.WireGuardInterfacesServiceClient, desired []*interfacesv1.WireGuardPeer) *interfacesv1.WireGuardPeerSyncResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	reply, err := client.SyncPeers(ctx, &interfacesv1.WireGuardSyncPeersRequest{
		Name: interfaceName, Desired: &interfacesv1.WireGuardPeerSet{Peers: desired},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reply
}

func wgOutput(t *testing.T, field string) string {
	t.Helper()
	output, err := exec.CommandContext(t.Context(), "wg", "show", interfaceName, field).Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
}

func syncPeersHTTP(t *testing.T, f fixture, source []byte, want int) peerSyncReply {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, f.address+interfaceURL+"/peers", bytes.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("sync peer list returned %s, want %d", resp.Status, want)
	}
	var reply peerSyncReply
	if want == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
			t.Fatal(err)
		}
	}
	return reply
}
