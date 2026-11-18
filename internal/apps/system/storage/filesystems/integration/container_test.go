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
	"golang.org/x/sys/unix"
)

var image = flag.String("image", "", "prebuilt Node image to verify")

func TestPackagedFilesystems(t *testing.T) {
	if *image == "" {
		t.Fatal("-image must select the prebuilt checked image")
	}
	pki := testutil.NewPKI(t)
	config := nodeConfig(t, pki)
	// Only an owned directory is exposed, read-only, for an actual host
	// filesystem observation. No host-root/PID namespace or capabilities are added.
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
	dataDirectory := t.TempDir()
	var expected unix.Statfs_t
	if err := unix.Statfs(dataDirectory, &expected); err != nil {
		t.Fatal(err)
	}
	name := "node-filesystems-" + hex.EncodeToString(random[:])
	docker(t, "run", "--detach", "--pull=never", "--name", name, "--network=host", "--cpus=0.5", "--cap-drop=ALL", "--read-only",
		"--user", strconv.Itoa(os.Getuid())+":"+strconv.Itoa(os.Getgid()), "--mount", "type=bind,src="+directory+",dst=/etc/node,readonly", "--mount", "type=bind,src="+dataDirectory+",dst=/observed,readonly", *image)
	t.Cleanup(func() { docker(t, "rm", "--force", name) })
	addresses := containerAddresses(t, name)
	client := connectClients(t, pki.Client, config, addresses)

	// Inspect the owned container's real mount namespace from outside. This
	// proves the selected API path resolves the explicit read-only host bind.
	pid, err := strconv.Atoi(strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.Pid}}", name)))
	if err != nil || pid <= 0 {
		t.Fatal("invalid owned container PID")
	}
	id := mountID(t, "/proc/"+strconv.Itoa(pid)+"/mountinfo", "/observed")
	assertFilesystemReads(t, client, id, "/observed", "/observed", true, expected)

	docker(t, "stop", "--time", "6", name)
	if strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.ExitCode}}", name)) != "0" {
		t.Fatal("packaged capability did not shut down cleanly")
	}
}
