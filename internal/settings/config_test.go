package settings_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/settings"
)

func TestLoadResolvesIdentityAgainstConfigDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(validConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	// Deployment environment values do not override the explicit YAML file.
	t.Setenv("NODE_HTTP_ADDRESS", "bad-address")
	config, err := settings.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	http := config.Communications.HTTP
	if http == nil || http.Address != "127.0.0.1:0" || config.StartupTimeout != 5*time.Second {
		t.Fatal("configuration values changed")
	}
	if http.TLS.Certificate != filepath.Join(dir, "certs/node.crt") || http.TLS.PrivateKey != filepath.Join(dir, "certs/node.key") || http.TLS.CA != filepath.Join(dir, "certs/clients-ca.crt") {
		t.Fatal("relative TLS identity paths must follow the configuration file")
	}
	if _, err := os.Stat(http.TLS.PrivateKey); !os.IsNotExist(err) {
		t.Fatal("loader must not generate private key files")
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	for _, test := range []struct{ name, input string }{
		{"unknown key", validConfig + "unknown: true\n"},
		{"second document", validConfig + "---\nstartup_timeout: 2s\n"},
		{"duplicate key", validConfig + "startup_timeout: 3s\n"},
		{"missing address", strings.Replace(validConfig, "127.0.0.1:0", "", 1)},
		{"invalid port", strings.Replace(validConfig, "127.0.0.1:0", "127.0.0.1:70000", 1)},
		{"missing CA", strings.Replace(validConfig, "certs/clients-ca.crt", "", 1)},
		{"unbounded read", strings.Replace(validConfig, "read_timeout: 1s", "read_timeout: 0s", 1)},
		{"invalid drain budget", strings.Replace(validConfig, "shutdown_timeout: 4s", "shutdown_timeout: 10s", 1)},
		{"zero body limit", strings.Replace(validConfig, "body_limit: 1024", "body_limit: 0", 1)},
		{"oversized file", validConfig + "#" + strings.Repeat("x", 1<<20)},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yml")
			if err := os.WriteFile(path, []byte(test.input), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := settings.Load(path); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}

func TestLoadDoesNotEnableAnUnconfiguredTransport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("startup_timeout: 5s\nshutdown_timeout: 10s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := settings.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Communications.HTTP != nil || config.Communications.GRPC != nil || config.Communications.NATS != nil {
		t.Fatal("an unconfigured transport was enabled")
	}
}

func TestRelativeConfigPathProducesAbsoluteIdentityPaths(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("config.yml", []byte(validConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := settings.Load("config.yml")
	if err != nil {
		t.Fatal(err)
	}
	if config.Communications.HTTP.TLS.Certificate != filepath.Join(dir, "certs/node.crt") {
		t.Fatal("loaded identity still depends on the current working directory")
	}
}

func TestDeploymentExampleIsValid(t *testing.T) {
	if _, err := settings.Load("../../deploy/config.example.yml"); err != nil {
		t.Fatalf("deployment example: %v", err)
	}
}

func FuzzLoad(f *testing.F) {
	for _, seed := range []string{validConfig, grpcConfig, natsConfig, "", validConfig + "---\n", validConfig + "startup_timeout: 0s\n"} {
		f.Add(seed)
	}
	path := filepath.Join(f.TempDir(), "config.yml")
	f.Fuzz(func(t *testing.T, input string) {
		// The loader's 1 MiB boundary has a deterministic regression test. Bound
		// generated inputs here so fuzzing explores parsing rather than disk volume.
		if len(input) > 64<<10 {
			t.Skip()
		}
		if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _ = settings.Load(path)
	})
}
