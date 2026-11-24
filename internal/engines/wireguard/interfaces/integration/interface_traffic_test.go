package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"testing"
	"time"

	statsv1 "github.com/PapilioVPN/node/internal/engines/wireguard/stats/bridge/grpc/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type trafficCounters struct {
	RXBytes uint64 `json:"rx_bytes,string"`
	TXBytes uint64 `json:"tx_bytes,string"`
}

func trafficClient(t *testing.T, f fixture) statsv1.WireGuardStatsServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(f.grpc, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return statsv1.NewWireGuardStatsServiceClient(conn)
}

func assertInterfaceTraffic(t *testing.T, f fixture, client statsv1.WireGuardStatsServiceClient, active bool) {
	t.Helper()
	output, err := exec.CommandContext(t.Context(), "ip", "-j", "-s", "link", "show", "dev", interfaceName).Output()
	if err != nil {
		t.Fatal(err)
	}
	var links []struct {
		Stats *struct {
			RX struct{ Bytes uint64 }
			TX struct{ Bytes uint64 }
		} `json:"stats64"`
	}
	if err := json.Unmarshal(output, &links); err != nil || len(links) != 1 || links[0].Stats == nil {
		t.Fatalf("kernel traffic counters unavailable: %v", err)
	}
	want := trafficCounters{RXBytes: links[0].Stats.RX.Bytes, TXBytes: links[0].Stats.TX.Bytes}
	if active && (want.RXBytes == 0 || want.TXBytes == 0) {
		t.Fatalf("expected traffic in both directions, got %+v", want)
	}
	reply, err := f.http.Get(f.address + interfaceURL + "/traffic")
	if err != nil {
		t.Fatal(err)
	}
	defer reply.Body.Close()
	if reply.StatusCode != http.StatusOK {
		t.Fatalf("traffic HTTP status = %s", reply.Status)
	}
	var got trafficCounters
	if err := json.NewDecoder(reply.Body).Decode(&got); err != nil || got != want {
		t.Fatalf("HTTP traffic = %+v, %v, want %+v", got, err, want)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	rpc, err := client.GetInterfaceTraffic(ctx, &statsv1.WireGuardInterfaceTrafficRequest{Name: interfaceName})
	if err != nil || rpc.GetRxBytes() != want.RXBytes || rpc.GetTxBytes() != want.TXBytes {
		t.Fatalf("gRPC traffic = %v, %v, want %+v", rpc, err, want)
	}
}

func assertMissingInterfaceTraffic(t *testing.T, f fixture, client statsv1.WireGuardStatsServiceClient, name string) {
	t.Helper()
	reply, err := f.http.Get(f.address + "/v1/wireguard/interfaces/" + name + "/traffic")
	if err != nil {
		t.Fatal(err)
	}
	reply.Body.Close()
	if reply.StatusCode != http.StatusNotFound {
		t.Fatalf("traffic for %q returned %s, want NotFound", name, reply.Status)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := client.GetInterfaceTraffic(ctx, &statsv1.WireGuardInterfaceTrafficRequest{Name: name}); status.Code(err) != codes.NotFound {
		t.Fatalf("traffic for %q returned %v, want NotFound", name, err)
	}
}
