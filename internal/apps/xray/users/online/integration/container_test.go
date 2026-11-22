//go:build integration && distribution && linux

package integration_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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

var image = flag.String("image", "", "prebuilt Node image containing the online capability")

func TestPackagedOnlineCollections(t *testing.T) {
	if *image == "" {
		t.Fatal("-image must select the prebuilt image")
	}
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	brokerURL := strings.Replace(broker.ClientURL(), "nats://", "tls://", 1)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			target := echoTarget(t)
			f := newRuntimeFixture(t, pki, brokerURL, transport, startContainer)
			exerciseCollectionWorkflow(t, f, target)
		})
	}
}
func docker(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.WaitDelay = time.Second
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v %s", args[0], err, data)
	}
	return string(data)
}
func startContainer(t *testing.T, config settings.Config, log *listenerLog) {
	t.Helper()
	directory := filepath.Dir(config.Instances[0].Xray.Configuration)
	path := filepath.Join(directory, "node.yml")
	data, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	name := "node-online-" + hex.EncodeToString(id[:])
	args := []string{"run", "--detach", "--pull=never", "--name", name, "--network=host", "--cap-drop=ALL", "--read-only", "--user", strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()), "--mount", "type=bind,src=" + directory + ",dst=" + directory}
	binary := config.Instances[0].Xray.Binary
	args = append(args, "--mount", "type=bind,src="+binary+",dst="+binary+",readonly")
	var files mtls.Files
	if config.Communications.HTTP != nil {
		files = config.Communications.HTTP.TLS
	}
	if config.Communications.GRPC != nil {
		files = config.Communications.GRPC.TLS
	}
	if config.Communications.NATS != nil {
		files = config.Communications.NATS.TLS
	}
	pkiDir := filepath.Dir(files.Certificate)
	args = append(args, "--mount", "type=bind,src="+pkiDir+",dst="+pkiDir+",readonly", *image, "-config", path)
	docker(t, args...)
	t.Cleanup(func() {
		docker(t, "stop", "--time", "10", name)
		code := strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.ExitCode}}", name))
		if code != "0" {
			t.Error("packaged Node did not shut down cleanly", code, docker(t, "logs", name))
		}
		docker(t, "rm", "--force", name)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for {
		subscribed := false
		for line := range strings.SplitSeq(docker(t, "logs", name), "\n") {
			var event struct {
				Message string `json:"msg"`
				Address string `json:"address"`
			}
			if json.Unmarshal([]byte(line), &event) != nil {
				continue
			}
			if event.Address != "" {
				log.addresses[event.Message] = event.Address
			}
			if event.Message == "Xray online records NATS subscribed" {
				subscribed = true
			}
		}
		if (config.Communications.HTTP != nil && log.addresses["HTTP listening"] != "") || (config.Communications.GRPC != nil && log.addresses["gRPC listening"] != "") || (config.Communications.NATS != nil && subscribed) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("packaged online endpoint did not start", docker(t, "logs", name))
		case <-time.After(20 * time.Millisecond):
		}
	}
}
