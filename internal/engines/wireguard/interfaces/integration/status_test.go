package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	interfacesv1 "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/grpc/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type observation struct {
	Name string `json:"name"`
	MTU  uint32 `json:"mtu"`
	Up   bool   `json:"up"`
}

func TestInterfaceStatus(t *testing.T) {
	if os.Getenv(parentNetNS) == "" {
		if os.Getenv("NODE_TEST_WIREGUARD") != "1" {
			t.Skip("set NODE_TEST_WIREGUARD=1 for the isolated Linux kernel test")
		}
		cmd := exec.CommandContext(t.Context(), "unshare", "--user", "--map-root-user", "--net",
			os.Args[0], "-test.run=^TestInterfaceStatus$")
		cmd.Env = append(os.Environ(), parentNetNS+"="+networkNamespace(t))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated WireGuard test: %v\n%s", err, out)
		}
		return
	}

	f := newFixture(t)
	client := f.grpcClient(t)
	want := observation{Name: interfaceName, MTU: 1420}
	assertStatus(t, f, client, want)
	runIP(t, "link", "set", "dev", interfaceName, "mtu", "1380", "up")
	want.MTU, want.Up = 1380, true
	assertStatus(t, f, client, want)
	runIP(t, "link", "set", "dev", interfaceName, "down")
	runIP(t, "link", "set", "dev", interfaceName, "name", "wg-other")
	t.Cleanup(func() { runIP(t, "link", "set", "dev", "wg-other", "name", interfaceName) })
	for _, name := range []string{interfaceName, "wg-other"} {
		assertNotFound(t, f, client, name)
	}
	runIP(t, "link", "add", "dev", interfaceName, "type", "dummy")
	t.Cleanup(func() { runIP(t, "link", "del", "dev", interfaceName) })
	assertNotFound(t, f, client, interfaceName)
}

func assertStatus(t *testing.T, f fixture, client interfacesv1.WireGuardInterfacesServiceClient, want observation) {
	t.Helper()
	if got := f.readHTTP(t); got != want {
		t.Fatalf("HTTP status = %+v, want %+v", got, want)
	}
	if got := readGRPC(t, client); got != want {
		t.Fatalf("gRPC status = %+v, want %+v", got, want)
	}
}

func assertNotFound(t *testing.T, f fixture, client interfacesv1.WireGuardInterfacesServiceClient, name string) {
	t.Helper()
	resp, err := f.http.Get(f.address + "/v1/wireguard/interfaces/" + name)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("HTTP status for %s = %s", name, resp.Status)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, err = client.GetStatus(ctx, &interfacesv1.WireGuardInterfaceRequest{Name: name})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("gRPC status for %s = %v", name, err)
	}
}

func (f fixture) grpcClient(t testing.TB) interfacesv1.WireGuardInterfacesServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(f.grpc, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return interfacesv1.NewWireGuardInterfacesServiceClient(conn)
}

func readGRPC(t testing.TB, client interfacesv1.WireGuardInterfacesServiceClient) observation {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	reply, err := client.GetStatus(ctx, &interfacesv1.WireGuardInterfaceRequest{Name: interfaceName})
	if err != nil {
		t.Fatal(err)
	}
	return observation{Name: reply.GetName(), MTU: reply.GetMtu(), Up: reply.GetUp()}
}

func (f fixture) readHTTP(t testing.TB) observation {
	t.Helper()
	resp, err := f.http.Get(f.address + interfaceURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result observation
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("interface HTTP status = %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}
