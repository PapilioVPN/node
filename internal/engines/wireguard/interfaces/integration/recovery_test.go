package integration_test

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/settings"
)

const recoveryConfigEnv = "NODE_TEST_WIREGUARD_RECOVERY_CONFIG"

func TestRestartRecovery(t *testing.T) {
	if os.Getenv(parentNetNS) == "" {
		if os.Getenv("NODE_TEST_WIREGUARD") != "1" {
			t.Skip("set NODE_TEST_WIREGUARD=1 for the isolated Linux kernel test")
		}
		cmd := exec.CommandContext(t.Context(), "unshare", "--user", "--map-root-user", "--net",
			os.Args[0], "-test.run=^TestRestartRecovery$")
		cmd.Env = append(os.Environ(), parentNetNS+"="+networkNamespace(t))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated WireGuard restart test: %v\n%s", err, out)
		}
		return
	}
	requireIsolatedNetwork(t)
	runIP(t, "link", "set", "dev", "lo", "up")
	directory := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	httpAddr, grpcAddr := reserveAddresses(t)
	path := filepath.Join(t.TempDir(), "config.yml")
	contents := fmt.Sprintf("management:\n  http_address: %q\n  grpc_address: %q\nwireguard:\n  interface: %q\n  managed: true\n  state_directory: %q\n",
		httpAddr, grpcAddr, interfaceName, directory)
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	f := fixture{http: &http.Client{Timeout: time.Second}, address: "http://" + httpAddr, grpc: grpcAddr}
	t.Cleanup(f.http.CloseIdleConnections)
	running := launchRecoveryNode(t, f, path)
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(privateKey.Bytes())
	source := fmt.Appendf(nil, `{"private_key":%q,"addresses":["192.0.2.1/24"],"mtu":1380}`, key)
	executionRequest(t, f, http.MethodPut, source, http.StatusOK)
	port, err := exec.CommandContext(t.Context(), "wg", "show", interfaceName, "listen-port").Output()
	if err != nil {
		t.Fatal(err)
	}
	exchangePacket(t, base64.StdEncoding.EncodeToString(privateKey.PublicKey().Bytes()), strings.TrimSpace(string(port)), func(string) {
		source = bytes.Replace(source, []byte(`"mtu":1380`), []byte(`"mtu":1360`), 1)
		want := fmt.Sprintf("%x", sha256.Sum256(source))
		if status := executionRequest(t, f, http.MethodPut, source, http.StatusOK); status.AppliedSHA256 != want {
			t.Fatalf("live update returned %+v", status)
		}
		before, err := net.InterfaceByName(interfaceName)
		if err != nil {
			t.Fatal(err)
		}
		for _, signum := range []os.Signal{os.Kill, syscall.SIGTERM} {
			running(signum)
			running = launchRecoveryNode(t, f, path)
			if status := f.readHTTP(t); status.MTU != 1360 || status.AppliedSHA256 != want || !status.Up {
				t.Fatalf("recovery after %v returned %+v", signum, status)
			}
			if status := readGRPC(t, f.grpcClient(t)); status.AppliedSHA256 != want {
				t.Fatalf("gRPC recovery after %v returned %+v", signum, status)
			}
			if status := executionRequest(t, f, http.MethodPut, source, http.StatusOK); status.AppliedSHA256 != want {
				t.Fatalf("Start after recovery returned %+v", status)
			}
			after, err := net.InterfaceByName(interfaceName)
			if err != nil || after.Index != before.Index {
				t.Fatalf("recovery after %v changed the link: %v, %v", signum, after, err)
			}
		}
	})
	stateFile := filepath.Join(directory, "wireguard.state")
	stat, err := os.Stat(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0600 || bytes.Contains(stored, []byte(key)) {
		t.Fatal("invalid checkpoint permissions or persisted private key")
	}
	pending := filepath.Join(directory, "wireguard.pending")
	if err := os.WriteFile(pending, nil, 0600); err != nil {
		t.Fatal(err)
	}
	undurable := bytes.Replace(source, []byte(`"mtu":1360`), []byte(`"mtu":1340`), 1)
	executionRequest(t, f, http.MethodPut, undurable, http.StatusServiceUnavailable)
	if err := os.Remove(pending); err != nil {
		t.Fatal(err)
	}
	running(os.Kill)
	running = launchRecoveryNode(t, f, path)
	if status := f.readHTTP(t); status.MTU != 1340 || status.AppliedSHA256 != "" || !status.Up {
		t.Fatalf("recovery attested an unpersisted change: %+v", status)
	}
	executionRequest(t, f, http.MethodDelete, nil, http.StatusNoContent)
	running(syscall.SIGTERM)
	running = launchRecoveryNode(t, f, path)
	defer running(syscall.SIGTERM)
	if _, err := net.InterfaceByName(interfaceName); err == nil {
		t.Fatal("explicit Stop left a recoverable interface")
	}
	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Fatalf("explicit Stop left a checkpoint: %v", err)
	}
}

func TestRecoveryNodeProcess(t *testing.T) {
	path := os.Getenv(recoveryConfigEnv)
	if path == "" {
		return
	}
	requireIsolatedNetwork(t)
	cfg, err := settings.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(t.Context(), syscall.SIGTERM)
	defer cancel()
	if err := bootstrap.Run(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
}

func launchRecoveryNode(t *testing.T, f fixture, path string) func(os.Signal) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRecoveryNodeProcess$")
	cmd.Env = append(os.Environ(), recoveryConfigEnv+"="+path)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	stop := func(signum os.Signal) {
		t.Helper()
		if done == nil {
			return
		}
		_ = cmd.Process.Signal(signum)
		select {
		case err := <-done:
			done = nil
			if signum != os.Kill && err != nil {
				t.Fatalf("Node shutdown: %v\n%s", err, output.String())
			}
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			done = nil
			t.Fatal("Node failed to shut down")
		}
		f.http.CloseIdleConnections()
	}
	t.Cleanup(func() { stop(os.Kill) })
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			done = nil
			t.Fatalf("Node failed during startup: %v\n%s", err, output.String())
		default:
		}
		resp, err := f.http.Get(f.address + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusNoContent {
				return stop
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Node did not become ready")
	return stop
}

func reserveAddresses(t testing.TB) (string, string) {
	t.Helper()
	first, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	return first.Addr().String(), second.Addr().String()
}
