//go:build integration && distribution && linux

package integration_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"go.yaml.in/yaml/v3"
)

var image = flag.String("image", "", "prebuilt checked Node image")

func TestPackagedInstances(t *testing.T) {
	if *image == "" {
		t.Fatal("-image must select the prebuilt image")
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "instance-traffic-proof") }))
	defer target.Close()
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	first, second := engineFixture(t, "edge-a"), engineFixture(t, "edge-b")
	config := nodeConfig(t, pki, broker, "all", first, second)
	directory := filepath.Dir(pki.Server.Certificate)
	localIdentity := func(files mtls.Files) mtls.Files {
		return mtls.Files{Certificate: filepath.Base(files.Certificate), PrivateKey: filepath.Base(files.PrivateKey), CA: filepath.Base(files.CA)}
	}
	config.Communications.HTTP.TLS = localIdentity(pki.Server)
	config.Communications.GRPC.TLS = localIdentity(pki.Server)
	config.Communications.NATS.TLS = localIdentity(pki.Client)
	// These bind mounts select the actual executable and two distinct native
	// configs. The image needs no compiler, shell, engine download or root rights.
	for i, engine := range []engine{first, second} {
		config.Instances[i].Xray = &settings.Xray{Binary: "/opt/xray", Configuration: "/etc/xray/" + engine.settings.ID + "/xray.json", API: engine.settings.Xray.API}
	}
	saveConfig := func() {
		t.Helper()
		data, err := yaml.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "config.yml"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	saveConfig()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	baseName := "node-instances-" + hex.EncodeToString(random[:])
	arguments := func(name string) []string {
		return []string{"run", "--detach", "--pull=never", "--name", name, "--network=host", "--cap-drop=ALL", "--read-only", "--user", strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()), "--mount", "type=bind,src=" + directory + ",dst=/etc/node,readonly", "--mount", "type=bind,src=" + first.settings.Xray.Binary + ",dst=/opt/xray,readonly", "--mount", "type=bind,src=" + filepath.Dir(first.settings.Xray.Configuration) + ",dst=/etc/xray/edge-a,readonly", "--mount", "type=bind,src=" + filepath.Dir(second.settings.Xray.Configuration) + ",dst=/etc/xray/edge-b,readonly", *image}
	}
	docker(t, arguments(baseName)...)
	t.Cleanup(func() { docker(t, "rm", "--force", baseName) })
	addresses := containerAddresses(t, baseName)
	client := connectClients(t, pki.Client, config, addresses)
	for _, transport := range []string{"http", "grpc", "nats"} {
		exerciseLifecycle(t, client, transport, first, second, target.URL)
	}
	docker(t, "stop", "--time", "15", baseName)
	if strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.ExitCode}}", baseName)) != "0" {
		t.Fatal("packaged Node failed to stop cleanly")
	}
	assertEnginePortsClosed(t, first, second)

	// Force startup to expire after child execution and listeners have started.
	// Fx can skip OnStop with the expired context; process-final io.Closer must
	// still join both children before this actual PID1 exits with failure.
	stalled, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var sockets []net.Conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := stalled.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			sockets = append(sockets, conn)
			mu.Unlock()
		}
	}()
	defer func() {
		stalled.Close()
		<-done
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range sockets {
			conn.Close()
		}
	}()
	config.StartupTimeout = time.Second
	config.Communications.NATS.Servers = []string{"tls://" + stalled.Addr().String()}
	config.Communications.NATS.ConnectTimeout = 3 * time.Second
	saveConfig()
	failed := baseName + "-rollback"
	docker(t, arguments(failed)...)
	t.Cleanup(func() { docker(t, "rm", "--force", failed) })
	// Observe real child traffic while NATS startup is stalled, proving the test
	// exercises cleanup of a live child rather than a pre-spawn rejection.
	deadline := time.NewTimer(900 * time.Millisecond)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	observed := false
	for !observed {
		if traffic(first.proxy, target.URL) == nil {
			observed = true
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("did not observe child before startup failure")
		case <-tick.C:
		}
	}
	if strings.TrimSpace(docker(t, "wait", failed)) != "1" {
		t.Fatal("expired startup must fail Node")
	}
	assertEnginePortsClosed(t, first, second)
}

func assertEnginePortsClosed(t *testing.T, engines ...engine) {
	t.Helper()
	for _, engine := range engines {
		for _, address := range []string{engine.proxy, engine.api} {
			conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
			if err == nil {
				conn.Close()
				t.Fatalf("owned endpoint still listening after Node exit: %s", address)
			}
		}
	}
}
func docker(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", args...)
	command.WaitDelay = time.Second
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v %s", args[0], err, data)
	}
	return string(data)
}
func containerAddresses(t *testing.T, name string) map[string]string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		addresses := map[string]string{}
		subscribed := false
		for line := range strings.SplitSeq(docker(t, "logs", "--tail", "200", name), "\n") {
			var event struct {
				Message string `json:"msg"`
				Address string `json:"address"`
			}
			if json.Unmarshal([]byte(line), &event) != nil {
				continue
			}
			if event.Message == "HTTP listening" || event.Message == "gRPC listening" {
				addresses[event.Message] = event.Address
			}
			if event.Message == "instance readiness NATS subscribed" {
				subscribed = true
			}
		}
		if addresses["HTTP listening"] != "" && addresses["gRPC listening"] != "" && subscribed {
			return addresses
		}
		select {
		case <-ctx.Done():
			t.Fatal("packaged instance endpoints not ready")
		case <-tick.C:
		}
	}
}
