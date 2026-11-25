package integration_test

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	interfacesv1 "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/grpc/proto/v1"
	statsv1 "github.com/PapilioVPN/node/internal/engines/wireguard/stats/bridge/grpc/proto/v1"
)

func TestSampledPeerRemoval(t *testing.T) {
	if os.Getenv(parentNetNS) == "" {
		if os.Getenv("NODE_TEST_WIREGUARD") != "1" {
			t.Skip("requires an isolated network namespace")
		}
		cmd := exec.CommandContext(t.Context(), "unshare", "-Urn", os.Args[0], "-test.run=^TestSampledPeerRemoval$")
		cmd.Env = append(os.Environ(), parentNetNS+"="+networkNamespace(t))
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated peer removal: %v\n%s", err, output)
		}
		return
	}
	f := startFixtureConfigured(t, true, "  peer_removal_usage: sampled\n")
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Appendf(nil, `{"private_key":%q,"addresses":["192.0.2.1/24"]}`, base64.StdEncoding.EncodeToString(privateKey.Bytes()))
	executionRequest(t, f, http.MethodPut, body, http.StatusOK)
	client, stats := f.grpcClient(t), trafficClient(t, f)
	var key []byte
	var accounted trafficCounters
	var sampledAt time.Time
	exchangePacket(t, base64.StdEncoding.EncodeToString(privateKey.PublicKey().Bytes()), strings.TrimSpace(wgOutput(t, "listen-port")), func(peer string, ping func()) {
		key, err = base64.StdEncoding.DecodeString(peer)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
		defer cancel()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			usage, err := stats.GetPeerUsage(ctx, &statsv1.WireGuardPeerTrafficRequest{Name: interfaceName, PublicKey: key})
			if err != nil {
				t.Fatal(err)
			}
			if peer := usage.GetPeer(); peer != nil {
				accounted = trafficCounters{RXBytes: peer.GetRxBytes(), TXBytes: peer.GetTxBytes()}
				sampledAt = usage.GetSampledAt().AsTime()
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("accounting did not sample the active tunnel")
			case <-ticker.C:
			}
		}
		ping()
	})
	if live := readPeerStatsHTTP(t, f).Peers[0]; live.RXBytes <= accounted.RXBytes || live.TXBytes <= accounted.TXBytes {
		t.Fatal("expected new live traffic after the sample")
	}
	request := &interfacesv1.WireGuardRemovePeerRequest{Name: interfaceName, PublicKey: key}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := client.RemovePeer(ctx, request); err != nil {
		t.Fatal(err)
	}
	if got := wgOutput(t, "peers"); got != "" {
		t.Fatalf("acknowledged removal left a peer: %q", got)
	}
	usage, err := stats.GetPeerUsage(ctx, &statsv1.WireGuardPeerTrafficRequest{Name: interfaceName, PublicKey: key})
	if err != nil {
		t.Fatal(err)
	}
	assertUsageRow(t, usage.GetPeer(), key, accounted)
	if !usage.GetSampledAt().AsTime().Equal(sampledAt) {
		t.Fatal("removal replaced the periodic sample timestamp")
	}
	assertPeerActivity(t, f, stats, key, false)
}

type sampledPeer struct {
	PublicKey []byte `json:"public_key"`
	trafficCounters
}

type sampledPeerUsage struct {
	Peer      *sampledPeer  `json:"peer"`
	Peers     []sampledPeer `json:"peers"`
	SampledAt *time.Time    `json:"sampled_at"`
}

func assertSampledPeerRead(t *testing.T, f fixture, client statsv1.WireGuardStatsServiceClient, encoded string) ([]byte, trafficCounters) {
	t.Helper()
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	before := readPeerStatsHTTP(t, f).Peers[0]
	want := trafficCounters{RXBytes: before.RXBytes, TXBytes: before.TXBytes}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	input := &statsv1.WireGuardInterfaceTrafficRequest{Name: interfaceName}
	for {
		result, err := client.ListPeerUsage(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.GetPeers()) > 0 {
			if len(result.GetPeers()) != 1 || result.GetSampledAt() == nil {
				t.Fatalf("sampled group = %v", result)
			}
			assertUsageRow(t, result.GetPeers()[0], key, want)
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("periodic sample did not capture active peer traffic")
		case <-ticker.C:
		}
	}
	path := "/peers/" + base64.RawURLEncoding.EncodeToString(key) + "/usage"
	assertPeerActivity(t, f, client, key, true)
	result := readSampledUsageHTTP(t, f, http.MethodGet, path)
	if result.Peer == nil || !bytes.Equal(result.Peer.PublicKey, key) || result.Peer.trafficCounters != want {
		t.Fatalf("sampled single HTTP peer = %+v, want %+v", result.Peer, want)
	}
	request := &statsv1.WireGuardPeerTrafficRequest{Name: interfaceName, PublicKey: key}
	reset, err := client.ResetPeerUsage(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	assertUsageRow(t, reset.GetPeer(), key, want)
	if got := readSampledUsageHTTP(t, f, http.MethodPost, path+"/resets"); got.Peer != nil {
		t.Fatalf("second reset returned %+v", got.Peer)
	}
	return key, want
}

func assertSampledPeerRemoval(t *testing.T, f fixture, client statsv1.WireGuardStatsServiceClient, key []byte, want trafficCounters) {
	t.Helper()
	if want.RXBytes == 0 || want.TXBytes == 0 {
		t.Fatalf("expected unreported traffic before removal: %+v", want)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	retired, err := client.GetPeerUsage(ctx, &statsv1.WireGuardPeerTrafficRequest{Name: interfaceName, PublicKey: key})
	if err != nil {
		t.Fatal(err)
	}
	assertUsageRow(t, retired.GetPeer(), key, want)
	assertPeerActivity(t, f, client, key, false)
	reset := readSampledUsageHTTP(t, f, http.MethodPost, "/peers/usage/resets")
	if len(reset.Peers) != 1 || !bytes.Equal(reset.Peers[0].PublicKey, key) || reset.Peers[0].trafficCounters != want {
		t.Fatalf("group reset = %+v, want retired usage %+v", reset, want)
	}
	empty, err := client.ResetAllPeerUsage(ctx, &statsv1.WireGuardInterfaceTrafficRequest{Name: interfaceName})
	if err != nil || len(empty.GetPeers()) != 0 {
		t.Fatalf("second group reset = %v, %v", empty, err)
	}
}

func assertUsageRow(t *testing.T, row *statsv1.WireGuardUsageRow, key []byte, want trafficCounters) {
	t.Helper()
	if row == nil || !bytes.Equal(row.GetPublicKey(), key) || row.GetRxBytes() != want.RXBytes || row.GetTxBytes() != want.TXBytes {
		t.Fatalf("sampled usage = %v, want %+v", row, want)
	}
}

func readSampledUsageHTTP(t *testing.T, f fixture, method, path string) sampledPeerUsage {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, f.address+interfaceURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := f.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sampled usage %s %s = %s", method, path, resp.Status)
	}
	var result sampledPeerUsage
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}
