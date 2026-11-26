package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/settings"
)

type fixture struct {
	httpAddr, grpcAddr          string
	client                      *http.Client
	source                      []byte
	runtimeDir, dir, executable string
	port                        int
	clientNamespacePID          int
}

func newFixture(t testing.TB) fixture {
	t.Helper()
	exe := os.Getenv("NODE_TEST_OPENVPN")
	if exe == "" {
		t.Skip("set NODE_TEST_OPENVPN to the OpenVPN executable")
	}
	ns, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	if parent := os.Getenv("NODE_TEST_OPENVPN_PARENT_NETNS"); parent == "" || parent == ns {
		t.Fatal("run OpenVPN integration in an isolated network namespace")
	}
	command(t, "", "ip", "link", "set", "lo", "up")
	clientPID := separateClientNetwork(t)
	dir := t.TempDir()
	for _, name := range []string{"ca", "server", "client"} {
		cmd := []string{"req", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-noenc", "-subj", "/CN=fixture-" + name, "-keyout", name + ".key"}
		if name == "ca" {
			command(t, dir, "openssl", append(cmd, "-x509", "-days", "1", "-out", name+".crt")...)
		} else {
			command(t, dir, "openssl", append(cmd, "-out", name+".csr")...)
			command(t, dir, "openssl", "x509", "-req", "-in", name+".csr", "-CA", "ca.crt", "-CAkey", "ca.key",
				"-CAcreateserial", "-days", "1", "-out", name+".crt")
		}
	}
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(192, 0, 2, 1)})
	if err != nil {
		t.Fatal(err)
	}
	port := listener.LocalAddr().(*net.UDPAddr).Port
	listener.Close()
	source := fmt.Appendf(nil, `dev tun-node-s
proto udp4
local 192.0.2.1
port %d
disable-dco
topology subnet
server 10.224.0.0 255.255.255.0
ca ca.crt
cert server.crt
key server.key
dh none
verb 3
`, port)
	configFile := filepath.Join(dir, "openvpn.conf")
	if err := os.WriteFile(configFile, source, 0600); err != nil {
		t.Fatal(err)
	}
	// Keep the runtime directory short enough for Linux Unix socket paths.
	runtimeDir, err := os.MkdirTemp("", "node-ovpn-it-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	f := fixture{
		httpAddr: address(t), grpcAddr: address(t), source: source,
		client: &http.Client{Timeout: 5 * time.Second},
		dir:    dir, runtimeDir: runtimeDir, executable: exe, port: port, clientNamespacePID: clientPID,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- bootstrap.Run(ctx, settings.Config{
			Management: settings.Management{HTTPAddress: f.httpAddr, GRPCAddress: f.grpcAddr},
			OpenVPN:    settings.OpenVPN{ID: "primary", Executable: exe, ConfigFile: configFile, RuntimeDirectory: runtimeDir},
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	t.Cleanup(func() {
		f.client.CloseIdleConnections()
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(6 * time.Second):
			t.Error("Node did not stop")
		}
	})
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		if r, err := f.client.Get("http://" + f.httpAddr + "/healthz"); err == nil {
			r.Body.Close()
			if r.StatusCode == http.StatusNoContent {
				return f
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Node was not ready")
	return f
}

func separateClientNetwork(t testing.TB) int {
	t.Helper()
	anchor := exec.Command("unshare", "-n", "sleep", "infinity")
	if err := anchor.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = anchor.Process.Kill()
		_ = anchor.Wait()
		_ = exec.Command("ip", "link", "del", "ovpn-test-s").Run()
	})
	parent, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	pid := anchor.Process.Pid
	until := time.Now().Add(2 * time.Second)
	for time.Now().Before(until) {
		ns, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/net", pid))
		if err == nil && ns != parent {
			command(t, "", "ip", "link", "add", "ovpn-test-s", "type", "veth", "peer", "name", "ovpn-test-c")
			command(t, "", "ip", "link", "set", "ovpn-test-c", "netns", fmt.Sprint(pid))
			command(t, "", "ip", "addr", "add", "192.0.2.1/30", "dev", "ovpn-test-s")
			command(t, "", "ip", "link", "set", "ovpn-test-s", "up")
			command(t, "", "nsenter", "-t", fmt.Sprint(pid), "-n", "ip", "addr", "add", "192.0.2.2/30", "dev", "ovpn-test-c")
			command(t, "", "nsenter", "-t", fmt.Sprint(pid), "-n", "ip", "link", "set", "ovpn-test-c", "up")
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("client network namespace did not start")
	return 0
}

func address(t testing.TB) string {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func command(t testing.TB, dir, name string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", name, err, output)
	}
}

func (f fixture) request(t testing.TB, method, suffix string, body []byte, want int) []byte {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, "http://"+f.httpAddr+"/v1/openvpn/instances/primary"+suffix, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	reply, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer reply.Body.Close()
	raw, err := io.ReadAll(reply.Body)
	if err != nil || reply.StatusCode != want {
		t.Fatalf("%s %s = %d, want %d: %s (%v)", method, suffix, reply.StatusCode, want, raw, err)
	}
	return raw
}

func (f fixture) tunnel(t testing.TB) {
	t.Helper()
	log, err := os.Create(filepath.Join(f.dir, "client.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command("nsenter", "-t", fmt.Sprint(f.clientNamespacePID), "-n", f.executable,
		"--client", "--disable-dco", "--proto", "udp4", "--remote", "192.0.2.1",
		fmt.Sprint(f.port), "--nobind", "--dev", "tun-node-c", "--ca", "ca.crt", "--cert", "client.crt", "--key", "client.key")
	cmd.Dir, cmd.Stdout, cmd.Stderr = f.dir, log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	until := time.Now().Add(5 * time.Second)
	var output []byte
	for time.Now().Before(until) {
		output, _ = os.ReadFile(log.Name())
		if strings.Contains(string(output), "Initialization Sequence Completed") {
			f.ping(t)
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("OpenVPN client was not ready: %s", output)
}

func (f fixture) ping(t testing.TB) {
	t.Helper()
	command(t, f.dir, "nsenter", "-t", fmt.Sprint(f.clientNamespacePID), "-n", "ping",
		"-I", "tun-node-c", "-c", "1", "-W", "1", "10.224.0.1")
}
