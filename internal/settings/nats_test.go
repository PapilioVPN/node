package settings_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/settings"
)

const natsConfig = `startup_timeout: 5s
shutdown_timeout: 10s
communications:
  nats:
    servers: [tls://localhost:4222]
    tls:
      certificate: certs/node.crt
      private_key: certs/node.key
      ca: certs/broker-ca.crt
    tls_handshake_first: true
    connect_timeout: 1s
    shutdown_timeout: 2s
    write_timeout: 1s
    reconnect_wait: 1s
    reconnect_jitter: 200ms
    max_reconnects: 3
    reconnect_buffer_size: 1024
    ping_interval: 5s
    max_pings_outstanding: 2
`

func TestLoadNATSConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(natsConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := settings.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Communications.NATS.TLS.CA != filepath.Join(filepath.Dir(path), "certs/broker-ca.crt") {
		t.Fatal("NATS CA path does not follow configuration directory")
	}
}

func TestNATSRejectsInvalidConfiguration(t *testing.T) {
	for _, test := range []struct{ name, before, after string }{
		{"no brokers", "[tls://localhost:4222]", "[]"},
		{"insecure scheme", "tls://localhost:4222", "nats://localhost:4222"},
		{"embedded credential", "tls://localhost:4222", "tls://user:password@localhost:4222"},
		{"port", "tls://localhost:4222", "tls://localhost:99999"},
		{"zero port", "tls://localhost:4222", "tls://localhost:00"},
		{"url path", "tls://localhost:4222", "tls://localhost:4222/ignored"},
		{"query", "tls://localhost:4222", "tls://localhost:4222?token=secret"},
		{"connect", "connect_timeout: 1s", "connect_timeout: 0s"},
		{"drain", "shutdown_timeout: 2s", "shutdown_timeout: 0s"},
		{"write", "write_timeout: 1s", "write_timeout: 0s"},
		{"reconnect count", "max_reconnects: 3", "max_reconnects: -1"},
		{"reconnect buffer", "reconnect_buffer_size: 1024", "reconnect_buffer_size: 0"},
		{"reconnect wait", "reconnect_wait: 1s", "reconnect_wait: 0s"},
		{"reconnect jitter", "reconnect_jitter: 200ms", "reconnect_jitter: -1s"},
		{"ping", "ping_interval: 5s", "ping_interval: 0s"},
		{"pending pings", "max_pings_outstanding: 2", "max_pings_outstanding: 0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yml")
			if err := os.WriteFile(path, []byte(strings.Replace(natsConfig, test.before, test.after, 1)), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := settings.Load(path); err == nil {
				t.Fatal("invalid NATS configuration accepted")
			}
		})
	}
}

func TestAllTransportShutdownBudgets(t *testing.T) {
	contents := validConfig + ("  grpc:" + strings.Split(grpcConfig, "  grpc:")[1]) + strings.Split(natsConfig, "communications:\n")[1]
	for _, test := range []struct {
		name, budget string
		valid        bool
	}{
		{"no cleanup margin", "8s", false},
		{"room for cleanup", "9s", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yml")
			if err := os.WriteFile(path, []byte(strings.Replace(contents, "shutdown_timeout: 10s", "shutdown_timeout: "+test.budget, 1)), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := settings.Load(path)
			if (err == nil) != test.valid {
				t.Fatalf("combined shutdown budget validation: %v", err)
			}
		})
	}
}
