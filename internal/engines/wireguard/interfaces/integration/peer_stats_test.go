package integration_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	interfacesv1 "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/grpc/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type peerStatsReply struct {
	Peers []struct {
		PublicKey     []byte     `json:"public_key"`
		RXBytes       uint64     `json:"rx_bytes,string"`
		TXBytes       uint64     `json:"tx_bytes,string"`
		LastHandshake *time.Time `json:"last_handshake"`
	} `json:"peers"`
}

func assertMissingPeerStats(t *testing.T, client interfacesv1.WireGuardInterfacesServiceClient, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := client.ListPeerStats(ctx, &interfacesv1.WireGuardInterfaceRequest{Name: name}); status.Code(err) != codes.NotFound {
		t.Fatalf("statistics for %q returned %v, want NotFound", name, err)
	}
}

func assertActivePeerStats(t *testing.T, f fixture, client interfacesv1.WireGuardInterfacesServiceClient, key string) {
	t.Helper()
	observed := readPeerStatsHTTP(t, f)
	transfer := strings.Fields(wgOutput(t, "transfer"))
	handshake := strings.Fields(wgOutput(t, "latest-handshakes"))
	if len(observed.Peers) != 1 || len(transfer) != 3 || len(handshake) != 2 {
		t.Fatal("expected exactly one peer in the managed tunnel")
	}
	rx, errRX := strconv.ParseUint(transfer[1], 10, 64)
	tx, errTX := strconv.ParseUint(transfer[2], 10, 64)
	unix, errTime := strconv.ParseInt(handshake[1], 10, 64)
	peer := observed.Peers[0]
	if errRX != nil || errTX != nil || errTime != nil || rx == 0 || tx == 0 || unix == 0 ||
		base64.StdEncoding.EncodeToString(peer.PublicKey) != key || peer.RXBytes != rx || peer.TXBytes != tx ||
		peer.LastHandshake == nil || peer.LastHandshake.Unix() != unix {
		t.Fatalf("HTTP statistics differ from the active kernel peer: %+v", peer)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	response, err := client.ListPeerStats(ctx, &interfacesv1.WireGuardInterfaceRequest{Name: interfaceName})
	if err != nil || len(response.GetPeers()) != 1 {
		t.Fatalf("gRPC statistics = %v, %v", response, err)
	}
	rpc := response.GetPeers()[0]
	if base64.StdEncoding.EncodeToString(rpc.GetPublicKey()) != key || rpc.GetRxBytes() != rx || rpc.GetTxBytes() != tx ||
		rpc.GetLastHandshake() == nil || !rpc.GetLastHandshake().AsTime().Equal(*peer.LastHandshake) {
		t.Fatalf("gRPC statistics differ from HTTP: %v", rpc)
	}
}

func readPeerStatsHTTP(t testing.TB, f fixture) peerStatsReply {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.address+interfaceURL+"/peers/stats", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := f.http.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("HTTP peer statistics returned %s", response.Status)
	}
	var result peerStatsReply
	decoder := json.NewDecoder(response.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}
