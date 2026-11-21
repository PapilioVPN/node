package settings_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/settings"
)

func TestInstancePathsAndNoEngineProvisioning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "node.yml")
	input := "startup_timeout: 2s\nshutdown_timeout: 5s\ninstances:\n  - id: edge-a\n    engine: xray\n    stop_timeout: 1s\n    xray:\n      binary: bin/xray\n      configuration: configs/xray.json\n      work_directory: private/work\n      state_directory: private/state\n      api:\n        network: unix\n        address: run/api.sock\n"
	if err := os.WriteFile(path, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := settings.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	x := config.Instances[0].Xray
	if x.Binary != filepath.Join(dir, "bin/xray") || x.Configuration != filepath.Join(dir, "configs/xray.json") || x.API.Address != filepath.Join(dir, "run/api.sock") || x.WorkDirectory != filepath.Join(dir, "private/work") || x.StateDirectory != filepath.Join(dir, "private/state") {
		t.Fatalf("paths: %+v", x)
	}
	if _, err := os.Stat(x.Configuration); !os.IsNotExist(err) {
		t.Fatal("loader provisioned engine config")
	}
	if _, err := os.Stat(x.WorkDirectory); !os.IsNotExist(err) {
		t.Fatal("loader provisioned native workspace")
	}
	if _, err := os.Stat(x.StateDirectory); !os.IsNotExist(err) {
		t.Fatal("loader provisioned desired state directory")
	}
	if config.RequestDrainBudget() != 4*time.Second {
		t.Fatal("instance drain not reserved")
	}
	for _, bad := range []string{strings.Replace(input, "      work_directory: private/work\n", "", 1), strings.Replace(input, "edge-a", "../bad", 1), strings.Replace(input, "engine: xray", "engine: arbitrary", 1), strings.Replace(input, "stop_timeout: 1s", "stop_timeout: 5s", 1), strings.Replace(input, "network: unix\n        address: run/api.sock", "network: tcp\n        address: 0.0.0.0:1234", 1)} {
		os.WriteFile(path, []byte(bad), 0600)
		if _, err := settings.Load(path); err == nil {
			t.Fatal("invalid instance settings accepted")
		}
	}
}
