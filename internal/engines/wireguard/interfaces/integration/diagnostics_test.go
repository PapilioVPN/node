package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	diagnosticsv1 "github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics/bridge/grpc/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func assertDiagnostics(t *testing.T, f fixture) {
	t.Helper()
	conn, err := grpc.NewClient(f.grpc, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := diagnosticsv1.NewWireGuardDiagnosticsServiceClient(conn)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	req := &diagnosticsv1.WireGuardDiagnosticsRequest{Name: interfaceName}
	stream, err := client.GetLogs(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	event, err := stream.Recv()
	if err != nil || event.GetKind() != "started" || event.GetAt() == nil || event.GetSequence() == 0 {
		t.Fatalf("WireGuard log = %v, %v", event, err)
	}
	got, err := client.GetRuntimeStats(ctx, req)
	if err != nil || got.GetHeapBytes() == 0 || got.GetGoroutines() == 0 || got.UptimeSeconds == nil {
		t.Fatalf("WireGuard runtime = %v, %v", got, err)
	}
	resp, err := f.http.Get(f.address + interfaceURL + "/runtime")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var stats struct {
		HeapBytes uint64  `json:"heap_bytes,string"`
		Uptime    *uint64 `json:"uptime_seconds,string"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil || resp.StatusCode != 200 || stats.HeapBytes == 0 || stats.Uptime == nil {
		t.Fatalf("HTTP runtime = %+v, %s, %v", stats, resp.Status, err)
	}
	probe, err := f.http.Get(f.address + interfaceURL + "/egress-probe")
	if err != nil {
		t.Fatal(err)
	}
	probe.Body.Close()
	if probe.StatusCode != 503 {
		t.Fatalf("disabled egress probe returned %s", probe.Status)
	}
}
