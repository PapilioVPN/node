package integration_test

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	interfacesv1 "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/grpc/proto/v1"
)

func TestWireGuardLeaseAndLinkRepair(t *testing.T) {
	if os.Getenv(parentNetNS) == "" {
		if os.Getenv("NODE_TEST_WIREGUARD") != "1" {
			t.Skip("requires an isolated network namespace")
		}
		cmd := exec.CommandContext(t.Context(), "unshare", "-Urn", os.Args[0], "-test.run=^TestWireGuardLeaseAndLinkRepair$")
		cmd.Env = append(os.Environ(), parentNetNS+"="+networkNamespace(t))
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated lease and repair: %v\n%s", err, output)
		}
		return
	}
	f := startFixture(t, true)
	payload := []byte(`{"private_key":"AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=","addresses":["192.0.2.1/24"]}`)
	type result struct {
		Up          bool       `json:"up"`
		ExpiresAt   *time.Time `json:"keep_alive_expires_at"`
		Recreated   bool       `json:"recreated"`
		Index       int        `json:"interface_index"`
		NodeVersion string     `json:"node_version"`
		Kernel      string     `json:"kernel_release"`
	}
	start := func(query string) result {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, f.address+interfaceURL+"/execution"+query, bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := f.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Start returned %s", resp.Status)
		}
		var state result
		if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	first := start("?keep_alive_seconds=1")
	if !first.Up || first.ExpiresAt == nil || first.NodeVersion == "" || first.Kernel == "" || first.Recreated {
		t.Fatalf("first Start: %+v", first)
	}
	before, err := net.InterfaceByName(interfaceName)
	if err != nil {
		t.Fatal(err)
	}
	runIP(t, "link", "set", "dev", interfaceName, "mtu", "1320", "down")
	repaired := start("")
	now, err := net.InterfaceByName(interfaceName)
	if err != nil || now.Index != before.Index || now.MTU != 1420 || !repaired.Up || repaired.Recreated ||
		repaired.ExpiresAt == nil || !repaired.ExpiresAt.After(*first.ExpiresAt) {
		t.Fatalf("repair: %+v, link %v, %v", repaired, now, err)
	}
	client := f.grpcClient(t)
	request := &interfacesv1.WireGuardStartRequest{Name: interfaceName, ConfigJson: payload, KeepAliveSeconds: new(uint32)}
	disarmed, err := client.Start(t.Context(), request)
	if err != nil || disarmed.GetKeepAliveExpiresAt() != nil || disarmed.GetNodeVersion() != first.NodeVersion {
		t.Fatalf("gRPC lease disable: %v, %v", disarmed, err)
	}
	if armed := start("?keep_alive_seconds=1"); armed.ExpiresAt == nil {
		t.Fatal("lease was not rearmed")
	}
	runIP(t, "link", "del", "dev", interfaceName)
	recreated := start("")
	if !recreated.Recreated || !recreated.Up || recreated.ExpiresAt == nil || recreated.Index == first.Index {
		t.Fatalf("recreation: %+v", recreated)
	}
	deadline := recreated.ExpiresAt.Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := net.InterfaceByName(interfaceName); err != nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("the interface survived its management lease")
}
