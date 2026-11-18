package settings_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/settings"
)

const validConfig = `startup_timeout: 5s
shutdown_timeout: 10s
communications:
  http:
    address: 127.0.0.1:0
    tls:
      certificate: certs/node.crt
      private_key: certs/node.key
      ca: certs/clients-ca.crt
    read_timeout: 1s
    write_timeout: 2s
    idle_timeout: 3s
    shutdown_timeout: 4s
    body_limit: 1024
    concurrency: 8
`

func hostReadConfig() settings.Config {
	return settings.Config{StartupTimeout: time.Second, ShutdownTimeout: 5 * time.Second,
		Communications: settings.Communications{Requests: settings.Requests{Timeout: time.Second}},
		System:         settings.System{Host: settings.Host{Identity: &settings.ReadAccess{}}}}
}

func loadConfiguration(t *testing.T, input string) (settings.Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	return settings.Load(path)
}

const commonNATSRequests = `  requests:
    timeout: 100ms
    nats:
      subject_prefix: fixture.node-a
      pending_messages: 8
      pending_bytes: 8192
      shutdown_timeout: 1s
`
