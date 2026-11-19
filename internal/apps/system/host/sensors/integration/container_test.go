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

func TestPackagedSensors(t *testing.T) {
	if *image == "" {
		t.Fatal("-image must select the prebuilt checked image")
	}
	pki := testutil.NewPKI(t)
	config := nodeConfig(t, pki)
	// Bind controlled sysfs class inputs; no hardware state or host policy changes.
	sources := t.TempDir()
	put := func(name, value string) {
		path := filepath.Join(sources, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	put("hwmon/hwmon0/name", "fixture-chip")
	put("hwmon/hwmon0/temp1_input", "-12500")
	put("hwmon/hwmon0/temp1_max", "0")
	put("hwmon/hwmon0/fan1_input", "9007199254740993")
	put("power_supply/BAT0/uevent", "POWER_SUPPLY_NAME=BAT0\nPOWER_SUPPLY_TYPE=Battery\nPOWER_SUPPLY_PRESENT=0\nPOWER_SUPPLY_CAPACITY=0\n")
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
	name := "node-sensors-" + hex.EncodeToString(random[:])
	docker(t, "run", "--detach", "--pull=never", "--name", name, "--network=host", "--cpus=0.5", "--cap-drop=ALL", "--read-only",
		"--mount", "type=bind,src="+filepath.Join(sources, "hwmon")+",dst=/sys/class/hwmon,readonly",
		"--mount", "type=bind,src="+filepath.Join(sources, "power_supply")+",dst=/sys/class/power_supply,readonly",
		"--user", strconv.Itoa(os.Getuid())+":"+strconv.Itoa(os.Getgid()), "--mount", "type=bind,src="+directory+",dst=/etc/node,readonly", *image)
	t.Cleanup(func() { docker(t, "rm", "--force", name) })
	addresses := containerAddresses(t, name)
	client := connectClients(t, pki.Client, config, addresses)

	assertSensorReads(t, client, [][]string{{`"milli_celsius":"-12500"`, `"high_milli_celsius":"0"`}, {`"rpm":"9007199254740993"`}, {`"present":false`, `"capacity_percent":0`}})

	docker(t, "stop", "--time", "6", name)
	if strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.ExitCode}}", name)) != "0" {
		t.Fatal("packaged capability did not shut down cleanly")
	}
}
