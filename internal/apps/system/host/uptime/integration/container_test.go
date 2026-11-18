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

func TestPackagedHostUptime(t *testing.T) {
	if *image == "" {
		t.Fatal("-image must select the prebuilt checked image")
	}
	pki := testutil.NewPKI(t)
	config := nodeConfig(t, pki, "all")
	// Mount only private configuration and TLS files. Verify the actual runtime's
	// time namespace below, without host filesystem mounts or extra capabilities.
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
	name := "node-uptime-" + hex.EncodeToString(random[:])
	docker(t, "run", "--detach", "--pull=never", "--name", name, "--network=host", "--cap-drop=ALL", "--read-only",
		"--user", strconv.Itoa(os.Getuid())+":"+strconv.Itoa(os.Getgid()), "--mount", "type=bind,src="+directory+",dst=/etc/node,readonly", *image)
	t.Cleanup(func() { docker(t, "rm", "--force", name) })
	addresses := containerAddresses(t, name)
	client := connectClients(t, pki.Client, config, addresses)
	pid := strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.Pid}}", name))
	if value, err := strconv.Atoi(pid); err != nil || value <= 0 {
		t.Fatalf("invalid container PID: %q", pid)
	}
	// A distinct time namespace can still expose the exact host clock when its
	// boot-time offset is zero. Namespace inode equality is not required.
	offsets, err := os.ReadFile("/proc/" + pid + "/timens_offsets")
	if err != nil {
		t.Fatal(err)
	}
	foundBoot := false
	for line := range strings.SplitSeq(string(offsets), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "boottime" {
			continue
		}
		if foundBoot || len(fields) != 3 {
			t.Fatalf("invalid boot-time namespace offsets: %q", offsets)
		}
		seconds, secondsError := strconv.ParseInt(fields[1], 10, 64)
		nanos, nanosError := strconv.ParseInt(fields[2], 10, 64)
		if secondsError != nil || nanosError != nil || seconds != 0 || nanos != 0 {
			t.Fatalf("container clock has an offset from host boot time: %q", offsets)
		}
		foundBoot = true
	}
	if !foundBoot {
		t.Fatalf("missing boot-time namespace offset: %q", offsets)
	}

	assertFreshReads(t, client)

	docker(t, "stop", "--time", "6", name)
	if strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.ExitCode}}", name)) != "0" {
		t.Fatal("packaged capability did not shut down cleanly")
	}
}
