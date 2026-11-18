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

func TestPackagedHostCPU(t *testing.T) {
	if *image == "" {
		t.Fatal("-image must select the prebuilt checked image")
	}
	pki := testutil.NewPKI(t)
	config := nodeConfig(t, pki)
	// Load averages remain system-wide without joining host PID/UTS/time
	// namespaces. CPU ticks also describe the host, independently of CPU quota.
	// Mount only private configuration/TLS; compare outside-host reads.
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
	name := "node-load-" + hex.EncodeToString(random[:])
	docker(t, "run", "--detach", "--pull=never", "--name", name, "--network=host", "--cpus=0.5", "--cap-drop=ALL", "--read-only",
		"--user", strconv.Itoa(os.Getuid())+":"+strconv.Itoa(os.Getgid()), "--mount", "type=bind,src="+directory+",dst=/etc/node,readonly", *image)
	t.Cleanup(func() { docker(t, "rm", "--force", name) })
	addresses := containerAddresses(t, name)
	client := connectClients(t, pki.Client, config, addresses)

	// Inspect the actual controller through this owned container's mount namespace.
	pid, err := strconv.Atoi(strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.Pid}}", name)))
	if err != nil || pid <= 0 {
		t.Fatalf("invalid owned container PID: %d %v", pid, err)
	}
	root := "/proc/" + strconv.Itoa(pid) + "/root/sys/fs/cgroup"
	limit, err := os.ReadFile(filepath.Join(root, "cpu.max"))
	if os.IsNotExist(err) {
		quota, quotaErr := os.ReadFile(filepath.Join(root, "cpu/cpu.cfs_quota_us"))
		period, periodErr := os.ReadFile(filepath.Join(root, "cpu/cpu.cfs_period_us"))
		if quotaErr != nil || periodErr != nil {
			t.Fatalf("read applied CPU quota: %v %v", quotaErr, periodErr)
		}
		limit = append(append(quota, ' '), period...)
		err = nil
	}
	if err != nil {
		t.Fatalf("read applied CPU controller: %v", err)
	}
	fields := strings.Fields(string(limit))
	if len(fields) != 2 {
		t.Fatalf("invalid CPU controller: %q", limit)
	}
	quota, quotaErr := strconv.ParseUint(fields[0], 10, 64)
	period, periodErr := strconv.ParseUint(fields[1], 10, 64)
	if quotaErr != nil || periodErr != nil || period == 0 || period%2 != 0 || quota != period/2 {
		t.Fatalf("half-CPU quota was not applied: %q", limit)
	}

	assertFreshReads(t, client)

	docker(t, "stop", "--time", "6", name)
	if strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.ExitCode}}", name)) != "0" {
		t.Fatal("packaged capability did not shut down cleanly")
	}
}
