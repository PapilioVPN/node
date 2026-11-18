//go:build integration && distribution && linux

package integration_test

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"go.yaml.in/yaml/v3"
)

var image = flag.String("image", "", "prebuilt Node image to verify")

func TestPackagedHostProcesses(t *testing.T) {
	if *image == "" {
		t.Fatal("-image must select the prebuilt checked image")
	}
	pki := testutil.NewPKI(t)
	config := nodeConfig(t, pki)
	// Host PID scope is explicit; Node receives no capabilities or host-root mount.
	// The test-owned process stays outside the container.
	directory := filepath.Dir(pki.Server.Certificate)
	localFiles := func(files mtls.Files) mtls.Files {
		return mtls.Files{Certificate: filepath.Base(files.Certificate), PrivateKey: filepath.Base(files.PrivateKey), CA: filepath.Base(files.CA)}
	}
	config.Communications.HTTP.TLS = localFiles(pki.Server)
	config.Communications.GRPC.TLS = localFiles(pki.Server)
	config.Communications.NATS.TLS = localFiles(pki.Client)
	contents, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "config.yml"), contents, 0600); err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "node-processes-" + hex.EncodeToString(random[:])
	docker(t, "run", "--detach", "--pull=never", "--name", name, "--network=host", "--pid=host", "--cpus=0.5", "--cap-drop=ALL", "--read-only",
		"--user", strconv.Itoa(os.Getuid())+":"+strconv.Itoa(os.Getgid()), "--mount", "type=bind,src="+directory+",dst=/etc/node,readonly", *image)
	t.Cleanup(func() { docker(t, "rm", "--force", name) })
	addresses := containerAddresses(t, name)
	client := connectClients(t, pki.Client, config, addresses)

	// Prove the applied PID namespace, rather than inferring it from flags.
	pid, err := strconv.Atoi(strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.Pid}}", name)))
	if err != nil || pid <= 0 {
		t.Fatal("invalid owned container PID")
	}
	actual, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/ns/pid")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.Readlink("/proc/self/ns/pid")
	if err != nil {
		t.Fatal(err)
	}
	if actual != expected {
		t.Fatal("host PID namespace was not applied")
	}
	assertProcessReads(t, client, startProcess(t))

	docker(t, "stop", "--time", "6", name)
	if strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.ExitCode}}", name)) != "0" {
		t.Fatal("packaged capability did not shut down cleanly")
	}
}
