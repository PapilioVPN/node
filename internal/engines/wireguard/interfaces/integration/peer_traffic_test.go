package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	statsv1 "github.com/PapilioVPN/node/internal/engines/wireguard/stats/bridge/grpc/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func assertPeerTraffic(t *testing.T, f fixture, client statsv1.WireGuardStatsServiceClient, key string, ping func()) {
	t.Helper()
	publicKey, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		t.Fatal(err)
	}
	path := "/peers/" + base64.RawURLEncoding.EncodeToString(publicKey) + "/traffic"
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request := &statsv1.WireGuardPeerTrafficRequest{Name: interfaceName, PublicKey: publicKey}
	before := readPeerStatsHTTP(t, f).Peers[0]
	want := trafficCounters{RXBytes: before.RXBytes, TXBytes: before.TXBytes}
	if got := readTrafficCountersHTTP(t, f, http.MethodGet, path, http.StatusOK); got != want {
		t.Fatalf("initial peer traffic = %+v, want %+v", got, want)
	}
	if response, err := client.GetPeerTraffic(ctx, request); err != nil || response.GetRxBytes() != want.RXBytes || response.GetTxBytes() != want.TXBytes {
		t.Fatalf("initial gRPC peer traffic = %v, %v, want %+v", response, err, want)
	}
	if got := readTrafficCountersHTTP(t, f, http.MethodPost, path+"/resets", http.StatusOK); got != want {
		t.Fatalf("reset peer traffic = %+v, want %+v", got, want)
	}
	if got := readTrafficCountersHTTP(t, f, http.MethodGet, path, http.StatusOK); got != (trafficCounters{}) {
		t.Fatalf("peer traffic after reset = %+v", got)
	}
	after := readPeerStatsHTTP(t, f).Peers[0]
	if after.RXBytes != before.RXBytes || after.TXBytes != before.TXBytes || !after.LastHandshake.Equal(*before.LastHandshake) {
		t.Fatal("logical reset changed kernel bytes or handshake")
	}
	ping()
	after = readPeerStatsHTTP(t, f).Peers[0]
	want = trafficCounters{RXBytes: after.RXBytes - before.RXBytes, TXBytes: after.TXBytes - before.TXBytes}
	if want.RXBytes == 0 || want.TXBytes == 0 {
		t.Fatalf("expected traffic after reset, got %+v", want)
	}
	if response, err := client.ResetPeerTraffic(ctx, request); err != nil || response.GetRxBytes() != want.RXBytes || response.GetTxBytes() != want.TXBytes {
		t.Fatalf("gRPC reset = %v, %v, want %+v", response, err, want)
	}
	if got := readTrafficCountersHTTP(t, f, http.MethodGet, path, http.StatusOK); got != (trafficCounters{}) {
		t.Fatalf("peer traffic after gRPC reset = %+v", got)
	}
	missing := sha256.Sum256([]byte("unconfigured WireGuard peer"))
	readTrafficCountersHTTP(t, f, http.MethodGet, "/peers/"+base64.RawURLEncoding.EncodeToString(missing[:])+"/traffic", http.StatusNotFound)
	readTrafficCountersHTTP(t, f, http.MethodGet, "/peers/invalid/traffic", http.StatusBadRequest)
	request.PublicKey = missing[:]
	if _, err := client.GetPeerTraffic(ctx, request); status.Code(err) != codes.NotFound {
		t.Fatalf("missing peer returned %v, want NotFound", err)
	}
	request.PublicKey = []byte{1}
	if _, err := client.ResetPeerTraffic(ctx, request); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("malformed public key returned %v, want InvalidArgument", err)
	}
}

func readTrafficCountersHTTP(t testing.TB, f fixture, method, path string, wantStatus int) trafficCounters {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, f.address+interfaceURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := f.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer reply.Body.Close()
	if reply.StatusCode != wantStatus {
		t.Fatalf("traffic %s %s = %s, want %d", method, path, reply.Status, wantStatus)
	}
	var result trafficCounters
	if wantStatus == http.StatusOK {
		if err := json.NewDecoder(reply.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func BenchmarkPeerTraffic(b *testing.B) {
	for _, count := range []int{1, 1000} {
		b.Run(fmt.Sprintf("peers-%d", count), func(b *testing.B) {
			f := startFixture(b, true)
			executionRequest(b, f, http.MethodPut, []byte(`{"private_key":"AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=","addresses":["192.0.2.1/24"]}`), http.StatusOK)
			// Keep large-fixture link deletion outside Node's shutdown deadline.
			b.Cleanup(func() { runIP(b, "link", "del", "dev", interfaceName) })
			setPeers(b, count)
			key := sha256.Sum256([]byte(strconv.Itoa(count - 1)))
			path := "/peers/" + base64.RawURLEncoding.EncodeToString(key[:]) + "/traffic"
			b.Run("SelectedHTTP", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if got := readTrafficCountersHTTP(b, f, http.MethodGet, path, http.StatusOK); got != (trafficCounters{}) {
						b.Fatalf("idle peer counters = %+v", got)
					}
				}
			})
			b.Run("FullHTTP", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					found := false
					for _, peer := range readPeerStatsHTTP(b, f).Peers {
						if len(peer.PublicKey) == len(key) && [32]byte(peer.PublicKey) == key {
							if peer.RXBytes != 0 || peer.TXBytes != 0 {
								b.Fatalf("idle peer counters = %+v", peer)
							}
							found = true
							break
						}
					}
					if !found {
						b.Fatal("full snapshot omitted the requested peer")
					}
				}
			})
			b.Run("ResetHTTP", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if got := readTrafficCountersHTTP(b, f, http.MethodPost, path+"/resets", http.StatusOK); got != (trafficCounters{}) {
						b.Fatalf("idle peer counters = %+v", got)
					}
				}
			})
		})
	}
}
