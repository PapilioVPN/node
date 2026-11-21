//go:build integration && distribution && linux

package integration_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"go.yaml.in/yaml/v3"
)

var image = flag.String("image", "", "prebuilt checked Node image")

func TestPackagedRuntimeStatistics(t *testing.T) {
	if *image == "" {
		t.Fatal("-image must select the prebuilt image")
	}
	pki := testutil.NewPKI(t)
	f := newFixture(t, pki, brokerFixture(t, pki), "all")
	config := f.config
	config.Instances = append(config.Instances[:0:0], config.Instances...)
	xray := *config.Instances[0].Xray
	config.Instances[0].Xray = &xray
	xray.Binary = "/opt/xray"
	xray.Configuration = "/etc/engine/config.json"
	relative := func(files mtls.Files) mtls.Files {
		return mtls.Files{Certificate: filepath.Base(files.Certificate), PrivateKey: filepath.Base(files.PrivateKey), CA: filepath.Base(files.CA)}
	}
	h, g, n := *config.Communications.HTTP, *config.Communications.GRPC, *config.Communications.NATS
	h.TLS = relative(h.TLS)
	g.TLS = relative(g.TLS)
	n.TLS = relative(n.TLS)
	config.Communications.HTTP = &h
	config.Communications.GRPC = &g
	config.Communications.NATS = &n
	directory := filepath.Dir(pki.Server.Certificate)
	data, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "config.yml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "node-statistics-" + hex.EncodeToString(random[:])
	docker(t, "run", "--detach", "--pull=never", "--name", name, "--network=host", "--cap-drop=ALL", "--read-only", "--user", strconv.Itoa(os.Getuid())+":"+strconv.Itoa(os.Getgid()),
		"--mount", "type=bind,src="+directory+",dst=/etc/node,readonly",
		"--mount", "type=bind,src="+f.config.Instances[0].Xray.Binary+",dst=/opt/xray,readonly",
		"--mount", "type=bind,src="+f.config.Instances[0].Xray.Configuration+",dst=/etc/engine/config.json,readonly", *image)
	t.Cleanup(func() { docker(t, "rm", "--force", name) })
	client := connect(t, f, containerAddresses(t, name))
	awaitInstanceReady(t, client, "http")
	for _, transport := range []string{"http", "grpc", "nats"} {
		assertNativeStatistics(t, readStatistics(t, client, transport, "edge-a"), 1)
	}
	controlInstance(t, client, "http", "stop")
	if stopped := readStatistics(t, client, "grpc", "edge-a"); stopped.Error != "failed_precondition" {
		t.Fatalf("packaged stopped engine produced facts %+v", stopped)
	}
	controlInstance(t, client, "nats", "start")
	awaitInstanceReady(t, client, "grpc")
	for _, transport := range []string{"http", "grpc", "nats"} {
		assertNativeStatistics(t, readStatistics(t, client, transport, "edge-a"), 2)
	}
	docker(t, "stop", "--time", "8", name)
	if strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.ExitCode}}", name)) != "0" {
		t.Fatal("packaged runtime statistics owner did not shut down cleanly")
	}
	connection, err := net.DialTimeout("tcp", f.native, 100*time.Millisecond)
	if err == nil {
		connection.Close()
		t.Fatal("native Xray API survived Node shutdown")
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
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
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
			if event.Message == "Xray runtime statistics NATS subscribed" {
				subscribed = true
			}
		}
		if addresses["HTTP listening"] != "" && addresses["gRPC listening"] != "" && subscribed {
			return addresses
		}
		select {
		case <-ctx.Done():
			t.Fatal("packaged statistics endpoints did not start")
		case <-ticker.C:
		}
	}
}
