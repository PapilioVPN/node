//go:build integration && linux

package integration_test

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/settings"
	"go.yaml.in/yaml/v3"
)

// TRAFFIC_TEST_IMAGE selects a prebuilt image for the same owning acceptance
// chains. Only fixture source/binary/PKI paths are mounted, read-only, with the
// image's normal entrypoint, dropped capabilities and explicit host networking.
func startContainer(t *testing.T, config settings.Config, image string) map[string]string {
	t.Helper()
	data, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "node.yml")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	name := "traffic-test-" + hex.EncodeToString(nonce[:])
	args := []string{"run", "--detach", "--name", name, "--network", "host", "--read-only", "--cap-drop", "ALL", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())}
	mounts := map[string]bool{directory: true, filepath.Dir(config.Instances[0].Xray.Configuration): true, config.Instances[0].Xray.Binary: true}
	if config.Communications.HTTP != nil {
		mounts[filepath.Dir(config.Communications.HTTP.TLS.Certificate)] = true
	}
	if config.Communications.GRPC != nil {
		mounts[filepath.Dir(config.Communications.GRPC.TLS.Certificate)] = true
	}
	if config.Communications.NATS != nil {
		mounts[filepath.Dir(config.Communications.NATS.TLS.Certificate)] = true
	}
	for source := range mounts {
		args = append(args, "--volume", source+":"+source+":ro")
	}
	args = append(args, image, "-config", path)
	command := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	}
	output, err := command(args...)
	if err != nil {
		t.Fatalf("fixture container start: %v %s", err, output)
	}
	t.Cleanup(func() {
		output, err := command("stop", "--time", "12", name)
		if err != nil {
			t.Errorf("fixture container stop: %v %s", err, output)
		}
		state, inspectErr := command("inspect", "--format", "{{.State.ExitCode}}", name)
		if inspectErr != nil || strings.TrimSpace(string(state)) != "0" {
			logs, _ := command("logs", "--tail", "20", name)
			t.Errorf("fixture container exit %s %v: %s", state, inspectErr, logs)
		}
		if output, err := command("rm", name); err != nil {
			t.Errorf("fixture container removal: %v %s", err, output)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	logs := exec.CommandContext(ctx, "docker", "logs", "--follow", name)
	reader, err := logs.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	logs.Stderr = logs.Stdout
	if err := logs.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	ready := make(chan map[string]string, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(reader)
		addresses := map[string]string{}
		sent := false
		for scanner.Scan() {
			var event struct {
				Message string `json:"msg"`
				Address string `json:"address"`
			}
			if json.Unmarshal(scanner.Bytes(), &event) != nil {
				continue
			}
			addresses[event.Message] = event.Address
			installed := (config.Communications.HTTP == nil || addresses["HTTP listening"] != "") && (config.Communications.GRPC == nil || addresses["gRPC listening"] != "")
			_, natsReady := addresses["NATS connected"]
			_, started := addresses["started"]
			installed = installed && (config.Communications.NATS == nil || natsReady) && started
			if installed && !sent {
				snapshot := map[string]string{}
				for k, v := range addresses {
					snapshot[k] = v
				}
				ready <- snapshot
				sent = true
			}
		}
		logs.Wait()
	}()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case result := <-ready:
		return result
	case <-done:
		output, _ := command("logs", name)
		t.Fatalf("fixture container exited before readiness: %s", output)
	case <-time.After(8 * time.Second):
		output, _ := command("logs", name)
		t.Fatalf("fixture container readiness timeout: %s", output)
	}
	return nil
}
