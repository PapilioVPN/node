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

func TestPackagedHostMemory(t *testing.T) {
	if *image == "" {
		t.Fatal("-image must select the prebuilt checked image")
	}
	pki := testutil.NewPKI(t)
	config := nodeConfig(t, pki)
	// A bounded container must report physical host RAM and swap, not its cgroup limits.
	// No host-root/proc mount or added Linux capability is needed.
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
	name := "node-memory-" + hex.EncodeToString(random[:])
	if hostTotal(t) <= 128<<20 {
		t.Fatal("packaged memory acceptance requires more than 128 MiB of host RAM")
	}
	docker(t, "run", "--detach", "--pull=never", "--name", name, "--network=host", "--memory=128m", "--memory-swap=128m", "--cap-drop=ALL", "--read-only",
		"--user", strconv.Itoa(os.Getuid())+":"+strconv.Itoa(os.Getgid()), "--mount", "type=bind,src="+directory+",dst=/etc/node,readonly", *image)
	t.Cleanup(func() { docker(t, "rm", "--force", name) })
	addresses := containerAddresses(t, name)
	client := connectClients(t, pki.Client, config, addresses)
	pid, err := strconv.Atoi(strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.Pid}}", name)))
	if err != nil || pid <= 0 {
		t.Fatalf("invalid owned container PID: %d %v", pid, err)
	}
	// Read the actual controller value through this container's mount namespace,
	// rather than treating Docker's requested HostConfig as proof of enforcement.
	root := "/proc/" + strconv.Itoa(pid) + "/root/sys/fs/cgroup"
	limit, err := os.ReadFile(filepath.Join(root, "memory.max"))
	if os.IsNotExist(err) {
		limit, err = os.ReadFile(filepath.Join(root, "memory/memory.limit_in_bytes"))
	}
	if err != nil {
		t.Fatalf("read container memory controller: %v", err)
	}
	bytes, err := strconv.ParseUint(strings.TrimSpace(string(limit)), 10, 64)
	if err != nil || bytes != 128<<20 {
		t.Fatalf("container memory limit was not applied: %q %v", limit, err)
	}

	assertFreshReads(t, client)

	docker(t, "stop", "--time", "6", name)
	if strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.ExitCode}}", name)) != "0" {
		t.Fatal("packaged capability did not shut down cleanly")
	}
}
