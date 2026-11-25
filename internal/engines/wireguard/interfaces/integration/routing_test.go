package integration_test

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestWireGuardHostRouting(t *testing.T) {
	if os.Getenv(parentNetNS) == "" {
		if os.Getenv("NODE_TEST_WIREGUARD") != "1" {
			t.Skip("requires an isolated network namespace")
		}
		cmd := exec.CommandContext(t.Context(), "unshare", "-Urn", os.Args[0], "-test.run=^TestWireGuardHostRouting$")
		cmd.Env = append(os.Environ(), parentNetNS+"="+networkNamespace(t))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated routing: %v\n%s", err, out)
		}
		return
	}
	requireIsolatedNetwork(t)
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("requires nftables CLI")
	}
	if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("0\n"), 0); err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(t.Context(), "unshare", "--net", "sh", "-c", "echo ready; read -r hold")
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close(); _ = child.Wait() }()
	if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("target namespace: %q, %v", line, err)
	}
	pid := fmt.Sprint(child.Process.Pid)
	run := func(argv ...string) string {
		t.Helper()
		out, err := exec.CommandContext(t.Context(), argv[0], argv[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v: %s", argv, err, out)
		}
		return string(out)
	}
	target := func(argv ...string) {
		run(append([]string{"nsenter", "--target", pid, "--net", "--", "ip"}, argv...)...)
	}
	runIP(t, "link", "add", "wg-egress", "type", "veth", "peer", "name", "wg-target", "netns", pid)
	runIP(t, "addr", "add", "198.18.50.1/30", "dev", "wg-egress")
	runIP(t, "link", "set", "wg-egress", "up")
	runIP(t, "route", "add", "default", "via", "198.18.50.2", "dev", "wg-egress")
	target("link", "set", "lo", "up")
	target("addr", "add", "203.0.113.9/32", "dev", "lo")
	target("addr", "add", "198.18.50.2/30", "dev", "wg-target")
	target("link", "set", "wg-target", "up")
	run("nft", "add", "table", "ip", "keep_fixture")
	f := startFixtureConfigured(t, true, "  routing:\n    enable_ipv4_forwarding: true\n    masquerade: true\n    policy_table: 20101\n    policy_priority: 19001\n    gateway: 198.18.50.2\n")
	config := []byte(`{"private_key":"AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=","addresses":["192.0.2.1/24"]}`)
	executionRequest(t, f, http.MethodPut, config, http.StatusOK)
	forward, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil || strings.TrimSpace(string(forward)) != "1" {
		t.Fatalf("forwarding = %q, %v", forward, err)
	}
	runIP(t, "route", "del", "default", "via", "198.18.50.2", "dev", "wg-egress")
	if got := run("ip", "rule", "show"); !strings.Contains(got, "iif "+interfaceName+" lookup 20101") {
		t.Fatalf("missing policy rule: %s", got)
	}
	exchangePacketTo(t, strings.TrimSpace(wgOutput(t, "public-key")), strings.TrimSpace(wgOutput(t, "listen-port")), "203.0.113.9", func(string, func()) {})
	executionRequest(t, f, http.MethodDelete, nil, http.StatusNoContent)
	if got := run("nft", "list", "tables"); strings.Contains(got, "node_wg_") || !strings.Contains(got, "keep_fixture") {
		t.Fatalf("unexpected cleanup: %s", got)
	}
	if got := run("ip", "rule", "show"); strings.Contains(got, "19001:") {
		t.Fatalf("policy rule survived Stop: %s", got)
	}
}
