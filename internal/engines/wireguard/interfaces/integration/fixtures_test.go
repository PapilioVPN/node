package integration_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/settings"
)

const (
	interfaceName = "wg-node-test"
	parentNetNS   = "NODE_TEST_WIREGUARD_PARENT_NETNS"
	interfaceURL  = "/v1/wireguard/interfaces/" + interfaceName
)

func networkNamespace(t testing.TB) string {
	t.Helper()
	path, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func requireIsolatedNetwork(t testing.TB) {
	t.Helper()
	parent := os.Getenv(parentNetNS)
	if parent == "" {
		t.Skip("run the fixture in an isolated network namespace")
	}
	if networkNamespace(t) == parent {
		t.Fatal("WireGuard fixtures require a separate network namespace")
	}
}

type fixture struct {
	http    *http.Client
	address string
	grpc    string
}

func newFixture(t testing.TB) fixture {
	t.Helper()
	requireIsolatedNetwork(t)
	runIP(t, "link", "set", "dev", "lo", "up")
	runIP(t, "link", "add", "dev", interfaceName, "type", "wireguard")
	t.Cleanup(func() { runIP(t, "link", "del", "dev", interfaceName) })
	runIP(t, "link", "set", "dev", interfaceName, "mtu", "1420")

	httpListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		httpListener.Close()
		t.Fatal(err)
	}
	httpAddress, grpcAddress := httpListener.Addr().String(), grpcListener.Addr().String()
	httpListener.Close()
	grpcListener.Close()

	path := filepath.Join(t.TempDir(), "config.yml")
	configText := fmt.Sprintf("management:\n  http_address: %q\n  grpc_address: %q\nwireguard:\n  interface: %q\n",
		httpAddress, grpcAddress, interfaceName)
	if err := os.WriteFile(path, []byte(configText), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := settings.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bootstrap.Run(ctx, config, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	client := &http.Client{Timeout: time.Second}
	t.Cleanup(client.CloseIdleConnections)
	origin := "http://" + httpAddress
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(origin + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusNoContent {
				return fixture{client, origin, grpcAddress}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Node did not become ready")
	return fixture{}
}

func runIP(t testing.TB, args ...string) {
	t.Helper()
	if output, err := exec.Command("ip", args...).CombinedOutput(); err != nil {
		t.Fatalf("ip %v: %v: %s", args, err, output)
	}
}
