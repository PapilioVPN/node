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

	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"go.yaml.in/yaml/v3"
)

var image = flag.String("image", "", "prebuilt checked Node image")

func TestPackagedConfigurationValidation(t *testing.T) {
	if *image == "" {
		t.Fatal("-image must select the prebuilt image")
	}
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	f := nodeFixture(t, pki, broker, "all")
	config := f.config
	config.Instances = append([]settings.Instance(nil), f.config.Instances...)
	x := *f.config.Instances[0].Xray
	config.Instances[0].Xray = &x
	config.Instances[0].Xray.Binary = "/opt/xray"
	config.Instances[0].Xray.Configuration = "/etc/engine/active.json"
	config.Instances[0].Xray.WorkDirectory = "/work"
	relative := func(files mtls.Files) mtls.Files {
		return mtls.Files{Certificate: filepath.Base(files.Certificate), PrivateKey: filepath.Base(files.PrivateKey), CA: filepath.Base(files.CA)}
	}
	// Each transport has its own settings pointer; copy before container path mapping.
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
	name := "node-configs-" + hex.EncodeToString(random[:])
	docker(t, "run", "--detach", "--pull=never", "--name", name, "--network=host", "--cap-drop=ALL", "--read-only", "--user", strconv.Itoa(os.Getuid())+":"+strconv.Itoa(os.Getgid()),
		"--mount", "type=bind,src="+directory+",dst=/etc/node,readonly",
		"--mount", "type=bind,src="+f.config.Instances[0].Xray.Binary+",dst=/opt/xray,readonly",
		"--mount", "type=bind,src="+filepath.Join(f.directory, "active.json")+",dst=/etc/engine/active.json,readonly",
		"--mount", "type=bind,src="+f.config.Instances[0].Xray.WorkDirectory+",dst=/work", *image)
	t.Cleanup(func() { docker(t, "rm", "--force", name) })
	client := connect(t, pki.Client, f.config, containerAddresses(t, name))
	awaitTraffic(t, f)
	for _, transport := range []string{"http", "grpc", "nats"} {
		exerciseValidation(t, client, transport, f)
		exerciseCompilation(t, client, transport, f)
	}
	docker(t, "stop", "--time", "12", name)
	if strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.ExitCode}}", name)) != "0" {
		t.Fatal("packaged validation owner failed shutdown")
	}
	conn, err := net.DialTimeout("tcp", f.proxy, 100*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Fatal("owned Xray endpoint remained after packaged Node stopped")
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
		validated, compiled := false, false
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
			if event.Message == "Xray configuration validation NATS subscribed" {
				validated = true
			}
			if event.Message == "Xray configuration compilation NATS subscribed" {
				compiled = true
			}
		}
		if addresses["HTTP listening"] != "" && addresses["gRPC listening"] != "" && validated && compiled {
			return addresses
		}
		select {
		case <-ctx.Done():
			t.Fatal("packaged configuration endpoint did not start")
		case <-tick.C:
		}
	}
}
