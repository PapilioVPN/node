//go:build integration && distribution && linux

package integration_test

import (
	"context"
	"encoding/base64"
	"flag"
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

func TestPackagedCredentialTools(t *testing.T) {
	if *image == "" {
		t.Fatal("-image must select the prebuilt image")
	}
	binary := os.Getenv("XRAY_TEST_BINARY")
	if !filepath.IsAbs(binary) {
		t.Fatal("XRAY_TEST_BINARY must be an absolute pinned executable path")
	}
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	brokerURL := strings.Replace(broker.ClientURL(), "nats://", "tls://", 1)
	directory := filepath.Dir(pki.Server.Certificate)
	config, err := settings.Load("../../../../../../deploy/config.example.yml")
	if err != nil {
		t.Fatal(err)
	}
	relative := func(files mtls.Files) mtls.Files {
		return mtls.Files{Certificate: filepath.Base(files.Certificate), PrivateKey: filepath.Base(files.PrivateKey), CA: filepath.Base(files.CA)}
	}
	config.Communications.HTTP.Address = "127.0.0.1:0"
	config.Communications.HTTP.TLS = relative(pki.Server)
	config.Communications.GRPC.Address = "127.0.0.1:0"
	config.Communications.GRPC.TLS = relative(pki.Server)
	config.Communications.NATS.Servers = []string{brokerURL}
	config.Communications.NATS.TLS = relative(pki.Client)
	config.Communications.Requests.NATS.SubjectPrefix = "fixture.credentials"
	config.Instances = []settings.Instance{{ID: "a", Engine: "xray", StopTimeout: 2 * time.Second, Xray: &settings.Xray{
		Binary: "/opt/xray", Configuration: "/etc/node/engine.json", WorkDirectory: "/work",
	}}}
	data, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "config.yml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "engine.json"), []byte(`{"log":{"loglevel":"none"},"outbounds":[{"protocol":"blackhole"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	runDocker := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		result, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s: %v %s", args[0], err, result)
		}
		return string(result)
	}
	container := strings.TrimSpace(runDocker("run", "--detach", "--pull=never", "--network=host", "--cap-drop=ALL", "--read-only",
		"--user", strconv.Itoa(os.Getuid())+":"+strconv.Itoa(os.Getgid()),
		"--mount", "type=bind,src="+directory+",dst=/etc/node,readonly",
		"--mount", "type=bind,src="+binary+",dst=/opt/xray,readonly",
		"--mount", "type=bind,src="+work+",dst=/work", *image))
	t.Cleanup(func() { runDocker("rm", "--force", container) })

	tick := time.NewTicker(30 * time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(config.StartupTimeout)
	defer deadline.Stop()
	var addresses map[string]string
	for {
		logs := runDocker("logs", container)
		addresses = listeningAddresses(logs)
		if addresses["HTTP listening"] != "" && addresses["gRPC listening"] != "" && strings.Contains(logs, "Xray credential UUID NATS subscribed") {
			break
		}
		select {
		case <-deadline.C:
			t.Fatalf("credential endpoints did not start: %s", logs)
		case <-tick.C:
		}
	}
	var expected string
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			fixture := &boundaryFixture{transport: transport}
			fixture.connect(t, pki, brokerURL, addresses)
			artifact := fixture.invoke(t, "a", "uuids", map[string]any{"name": "packaged-credentials"})
			if artifact.Error != "" || len(artifact.UUID) != 36 || artifact.Metadata.InstanceID != "a" {
				t.Fatal("credential tool did not return an installed native result", artifact.Error)
			}
			if expected == "" {
				expected = artifact.UUID
			} else if artifact.UUID != expected {
				t.Fatal("deterministic native UUID differs across transports")
			}
			certificate := fixture.invoke(t, "a", "tls-certificates", map[string]any{})
			result := fixture.invoke(t, "a", "tls-certificate-hashes", map[string]any{
				"certificate": base64.StdEncoding.EncodeToString([]byte(certificate.Certificate)),
			})
			if certificate.Error != "" || result.Error != "" || len(result.Hashes) != 1 {
				t.Fatal("native certificate generation or hashing failed", certificate.Error, result.Error)
			}
		})
	}
	files, err := os.ReadDir(work)
	if err != nil || len(files) != 0 {
		t.Fatal("packaged credential tools left staged bytes in the work directory", err)
	}
	runDocker("stop", "--time", "15", container)
	if strings.TrimSpace(runDocker("inspect", "--format", "{{.State.ExitCode}}", container)) != "0" {
		t.Fatal("packaged Node failed to stop cleanly")
	}
}
