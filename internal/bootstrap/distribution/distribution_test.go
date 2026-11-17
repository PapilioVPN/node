//go:build distribution && integration && linux

package distribution_test

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"go.yaml.in/yaml/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

var distributionArchive = flag.String("bundle", "", "absolute path to the prepared runtime archive")

func TestDistributionInstallsAndRuns(t *testing.T) {
	if !filepath.IsAbs(*distributionArchive) {
		t.Fatal("-bundle must name the prepared runtime archive")
	}
	if _, err := os.Stat(*distributionArchive); err != nil {
		t.Fatalf("runtime distribution is missing: %v", err)
	}
	archiveDirectory := filepath.Dir(*distributionArchive)
	archiveName := filepath.Base(*distributionArchive)
	distributionCommand(t, archiveDirectory, "sha256sum", "--check", "--strict", archiveName+".sha256")
	extracted := t.TempDir()
	distributionCommand(t, "", "tar", "-xzf", *distributionArchive, "-C", extracted)
	bundle := filepath.Join(extracted, "node")
	distributionCommand(t, bundle, "sha256sum", "--check", "--strict", "checksums.sha256")
	// The archive is an explicit runtime allowlist, not a source checkout export.
	allowed := map[string]bool{"image.tar": true, "image.ref": true, "revision": true, "checksums.sha256": true,
		"install.sh": true, "run.sh": true, "scripts/install/install.sh": true, "scripts/run/run.sh": true, "deploy/config.example.yml": true}
	if err := filepath.WalkDir(bundle, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(bundle, path)
		if err != nil {
			return err
		}
		if !allowed[relative] {
			return fmt.Errorf("unexpected runtime member: %s", relative)
		}
		delete(allowed, relative)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(allowed) != 0 {
		t.Fatalf("runtime members missing: %v", allowed)
	}

	installation := filepath.Join(t.TempDir(), "installed node")
	private := filepath.Join(installation, "private config, \"TLS\"")
	if err := os.MkdirAll(private, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(installation, "data")
	if err := os.WriteFile(sentinel, []byte("existing runtime data"), 0o600); err != nil {
		t.Fatal(err)
	}
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	config := foundationConfig(pki, broker.ClientURL())
	server := copyDistributionIdentity(t, private, "server", pki.Server)
	client := copyDistributionIdentity(t, private, "client", pki.Client)
	config.Communications.HTTP.TLS, config.Communications.GRPC.TLS = server, server
	config.Communications.NATS.TLS = client
	encoded, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(private, "config.yml")
	if err := os.WriteFile(configFile, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	preserved := distributionFiles(t, installation)
	distributionCommand(t, "", filepath.Join(bundle, "install.sh"), installation)
	assertDistributionFiles(t, preserved)
	image := strings.TrimSpace(string(readDistributionFile(t, filepath.Join(installation, "image.id"))))
	if actual := strings.TrimSpace(distributionCommand(t, "", "docker", "image", "inspect", "--format", "{{.Id}}", image)); actual != image {
		t.Fatal("installed image identity changed")
	}

	name := "papilio-node-distribution-" + strings.ToLower(rand.Text())
	started := false
	t.Cleanup(func() {
		output, err := distributionExecute("", "docker", "rm", "--force", name)
		if err != nil && started {
			t.Errorf("remove owned container: %v\n%s", err, output)
		}
	})
	distributionCommand(t, "", filepath.Join(installation, "run.sh"), private, name, "--detach", "--network", "host")
	started = true
	addresses := distributionReadiness(t, name)
	if broker.NumClients() != 1 {
		t.Fatal("packaged Node has no authenticated NATS connection")
	}
	identity, err := mtls.Client(pki.Client)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{TLSClientConfig: identity}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport, Timeout: 2 * time.Second}).Get("https://" + addresses.HTTP + "/unregistered")
	if err != nil {
		t.Fatal(err)
	}
	_, copyError := io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if copyError != nil || response.StatusCode != http.StatusNotFound {
		t.Fatal("packaged HTTP transport failed")
	}
	conn, err := grpc.NewClient(addresses.GRPC, grpc.WithTransportCredentials(credentials.NewTLS(identity)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_, err = healthv1.NewHealthClient(conn).Check(ctx, &healthv1.HealthCheckRequest{})
	cancel()
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("packaged gRPC transport failed: %v", err)
	}
	conn.Close()
	transport.CloseIdleConnections()
	if rootfs := strings.TrimSpace(distributionCommand(t, "", "docker", "inspect", "--format", "{{.HostConfig.ReadonlyRootfs}} {{.Path}}", name)); rootfs != "true /node" {
		t.Fatalf("runtime rootfs/PID 1 contract changed: %s", rootfs)
	}

	// Reinstall the exact bundle while Node is running: no private-file mutation
	// or implicit container restart is part of installation.
	distributionCommand(t, "", filepath.Join(bundle, "install.sh"), installation)
	assertDistributionFiles(t, preserved)
	if state := strings.TrimSpace(distributionCommand(t, "", "docker", "inspect", "--format", "{{.State.Running}}", name)); state != "true" {
		t.Fatal("installation stopped the running Node")
	}
	distributionCommand(t, "", "docker", "stop", "--time", "6", name)
	if state := strings.TrimSpace(distributionCommand(t, "", "docker", "inspect", "--format", "{{.State.Running}} {{.State.ExitCode}}", name)); state != "false 0" {
		t.Fatalf("packaged Node did not stop cleanly: %s", state)
	}
	deadline := time.Now().Add(time.Second)
	for broker.NumClients() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if broker.NumClients() != 0 {
		t.Fatal("packaged Node left its NATS connection open")
	}
	assertDistributionFiles(t, preserved)

	// Refuse a canonical root destination or an escaping installation-owned link.
	if _, err := distributionExecute("", filepath.Join(bundle, "install.sh"), "/tmp/.."); err == nil {
		t.Fatal("root installation destination accepted")
	}
	escape := t.TempDir()
	linked := t.TempDir()
	if err := os.Symlink(escape, filepath.Join(linked, "scripts")); err != nil {
		t.Fatal(err)
	}
	if _, err := distributionExecute("", filepath.Join(bundle, "install.sh"), linked); err == nil {
		t.Fatal("escaping installation symlink accepted")
	}
	if entries, err := os.ReadDir(escape); err != nil || len(entries) != 0 {
		t.Fatal("installer wrote through an escaping symlink")
	}

	// A damaged bundle must fail verification before changing an installation.
	selected := readDistributionFile(t, filepath.Join(installation, "image.id"))
	if err := os.WriteFile(filepath.Join(bundle, "image.ref"), []byte("tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := distributionExecute("", filepath.Join(bundle, "install.sh"), installation); err == nil {
		t.Fatal("damaged bundle was installed")
	}
	if string(readDistributionFile(t, filepath.Join(installation, "image.id"))) != string(selected) {
		t.Fatal("failed verification changed installed identity")
	}
	assertDistributionFiles(t, preserved)
}
