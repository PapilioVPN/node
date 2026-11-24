package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	interfacesv1 "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func assertPeerDelta(t *testing.T, f fixture, client interfacesv1.WireGuardInterfacesServiceClient, self []byte) {
	t.Helper()
	before := wgOutput(t, "latest-handshakes")
	first := sha256.Sum256([]byte("delta first peer"))
	second := sha256.Sum256([]byte("delta second peer"))
	path := "/peers/" + base64.RawURLEncoding.EncodeToString(first[:])
	source := []byte(`{"allowed_ips":["192.0.2.45/32"]}`)
	if got := peerDeltaHTTP(t, f, http.MethodPut, path, source, http.StatusOK); got != (peerSyncReply{Added: 1}) {
		t.Fatalf("single upsert returned %+v", got)
	}
	if got := peerDeltaHTTP(t, f, http.MethodPut, path, source, http.StatusOK); got != (peerSyncReply{}) {
		t.Fatalf("single replay returned %+v", got)
	}
	peerDeltaHTTP(t, f, http.MethodPut, "/peers/"+base64.RawURLEncoding.EncodeToString(self), source, http.StatusBadRequest)
	firstKey := base64.StdEncoding.EncodeToString(first[:])
	secondKey := base64.StdEncoding.EncodeToString(second[:])
	claim := fmt.Appendf(nil, `{"upsert":[{"public_key":%q,"allowed_ips":["192.0.2.45/32"]}]}`, secondKey)
	peerDeltaHTTP(t, f, http.MethodPatch, "/peers", claim, http.StatusConflict)
	if output := wgOutput(t, "allowed-ips"); !strings.Contains(output, firstKey+"\t192.0.2.45/32") || strings.Contains(output, secondKey) {
		t.Fatal("conflicting delta changed the kernel peer set")
	}
	transfer := fmt.Appendf(nil, `{"upsert":[{"public_key":%q,"allowed_ips":["192.0.2.45/32"]}],"remove":[%q]}`, secondKey, firstKey)
	if got := peerDeltaHTTP(t, f, http.MethodPatch, "/peers", transfer, http.StatusOK); got != (peerSyncReply{Added: 1, Removed: 1}) {
		t.Fatalf("delta transfer returned %+v", got)
	}
	if got := peerDeltaHTTP(t, f, http.MethodPatch, "/peers", transfer, http.StatusOK); got != (peerSyncReply{}) {
		t.Fatalf("delta replay returned %+v", got)
	}
	remove := "/peers/" + base64.RawURLEncoding.EncodeToString(second[:])
	peerDeltaHTTP(t, f, http.MethodDelete, remove, nil, http.StatusNoContent)
	peerDeltaHTTP(t, f, http.MethodDelete, remove, nil, http.StatusNoContent)
	if strings.Contains(wgOutput(t, "peers"), secondKey) {
		t.Fatal("single removal left a peer in the kernel")
	}
	peerDeltaHTTP(t, f, http.MethodPatch, "/peers", []byte(`{}`), http.StatusBadRequest)
	peerDeltaHTTP(t, f, http.MethodPut, "/peers/invalid", source, http.StatusBadRequest)

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	upsert := &interfacesv1.WireGuardPeer{PublicKey: first[:], AllowedIps: []string{"192.0.2.45/32"}}
	reply, err := client.UpsertPeer(ctx, &interfacesv1.WireGuardUpsertPeerRequest{Name: interfaceName, Peer: upsert})
	if err != nil || reply.GetAdded() != 1 {
		t.Fatalf("gRPC upsert returned %v, %v", reply, err)
	}
	upsert.AllowedIps = []string{"192.0.2.46/32"}
	_, err = client.ApplyPeerDelta(ctx, &interfacesv1.WireGuardApplyPeerDeltaRequest{Name: interfaceName,
		Upsert: []*interfacesv1.WireGuardPeer{upsert}, Remove: [][]byte{first[:]},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("duplicate delta target returned %v", err)
	}
	if got := wgOutput(t, "allowed-ips"); !strings.Contains(got, firstKey+"\t192.0.2.45/32") {
		t.Fatal("invalid delta changed the first peer")
	}
	reply, err = client.ApplyPeerDelta(ctx, &interfacesv1.WireGuardApplyPeerDeltaRequest{Name: interfaceName,
		Upsert: []*interfacesv1.WireGuardPeer{upsert}, Remove: [][]byte{second[:]},
	})
	if err != nil || reply.GetUpdated() != 1 || reply.GetRemoved() != 0 {
		t.Fatalf("gRPC delta returned %v, %v", reply, err)
	}
	if got := wgOutput(t, "allowed-ips"); !strings.Contains(got, firstKey+"\t192.0.2.46/32") {
		t.Fatal("gRPC delta missed the kernel prefix update")
	}
	_, err = client.ApplyPeerDelta(ctx, &interfacesv1.WireGuardApplyPeerDeltaRequest{Name: interfaceName, Remove: make([][]byte, peers.MaxPeers+1)})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("oversized delta returned %v", err)
	}
	if _, err := client.RemovePeer(ctx, &interfacesv1.WireGuardRemovePeerRequest{Name: interfaceName, PublicKey: first[:]}); err != nil {
		t.Fatal(err)
	}
	if got := wgOutput(t, "latest-handshakes"); got != before {
		t.Fatal("targeted changes affected the established peer or left a fixture peer behind")
	}
}

func peerDeltaHTTP(t *testing.T, f fixture, method, path string, source []byte, want int) peerSyncReply {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, f.address+interfaceURL+path, bytes.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := f.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("peer delta %s returned %s, want %d", method, resp.Status, want)
	}
	var result peerSyncReply
	if want == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
	}
	return result
}
