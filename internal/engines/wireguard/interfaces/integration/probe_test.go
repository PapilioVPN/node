package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	diagnosticsv1 "github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics/bridge/grpc/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestEgressProbe(t *testing.T) {
	if os.Getenv(parentNetNS) == "" {
		if os.Getenv("NODE_TEST_WIREGUARD") != "1" {
			t.Skip("requires an isolated network namespace")
		}
		cmd := exec.CommandContext(t.Context(), "unshare", "-Urn", os.Args[0], "-test.run=^TestEgressProbe$")
		cmd.Env = append(os.Environ(), parentNetNS+"="+networkNamespace(t))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated probe: %v\n%s", err, out)
		}
		return
	}
	requireIsolatedNetwork(t)
	runIP(t, "link", "set", "dev", "lo", "up")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Error("probe used a method other than HEAD")
		}
		w.Header().Set("Location", "/unrequested")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	f := startFixtureConfigured(t, true, fmt.Sprintf("  probe:\n    url: %q\n    interface: lo\n", server.URL))
	executionRequest(t, f, http.MethodPut, []byte(`{"private_key":"AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=","addresses":["192.0.2.1/24"]}`), http.StatusOK)
	resp, err := f.http.Get(f.address + interfaceURL + "/egress-probe")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var observed struct {
		Reachable  bool
		HTTPStatus int `json:"http_status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&observed); err != nil || resp.StatusCode != 200 || !observed.Reachable || observed.HTTPStatus != 307 {
		t.Fatalf("HTTP probe = %+v, %s, %v", observed, resp.Status, err)
	}
	conn, err := grpc.NewClient(f.grpc, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	client := diagnosticsv1.NewWireGuardDiagnosticsServiceClient(conn)
	result, err := client.ProbeEgress(ctx, &diagnosticsv1.WireGuardDiagnosticsRequest{Name: interfaceName})
	if err != nil || !result.GetReachable() || result.GetHttpStatus() != 307 || result.GetElapsedNanoseconds() == 0 {
		t.Fatalf("gRPC probe = %v, %v", result, err)
	}
	server.Close()
	result, err = client.ProbeEgress(ctx, &diagnosticsv1.WireGuardDiagnosticsRequest{Name: interfaceName})
	if err != nil || result.GetReachable() {
		t.Fatalf("closed egress = %v, %v", result, err)
	}
}
