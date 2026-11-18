package settings_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/settings"
)

const grpcConfig = `startup_timeout: 5s
shutdown_timeout: 10s
communications:
  requests:
    timeout: 100ms
  grpc:
    address: 127.0.0.1:0
    tls:
      certificate: certs/node.crt
      private_key: certs/node.key
      ca: certs/clients-ca.crt
    connection_timeout: 1s
    shutdown_timeout: 2s
    max_recv_message_size: 1024
    max_send_message_size: 1024
    max_connections: 16
    max_concurrent_streams: 8
    max_header_list_size: 8192
`

func TestLoadGRPCConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(grpcConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := settings.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Communications.GRPC.TLS.CA != filepath.Join(filepath.Dir(path), "certs/clients-ca.crt") {
		t.Fatal("gRPC CA path does not follow config directory")
	}
}

func TestGRPCRejectsUnboundedResources(t *testing.T) {
	for _, test := range []struct{ name, before, after string }{
		{"handshake", "connection_timeout: 1s", "connection_timeout: 0s"},
		{"handshake budget", "connection_timeout: 1s", "connection_timeout: 3s"},
		{"drain", "shutdown_timeout: 2s", "shutdown_timeout: 0s"},
		{"receive", "max_recv_message_size: 1024", "max_recv_message_size: 0"},
		{"send", "max_send_message_size: 1024", "max_send_message_size: -1"},
		{"connections", "max_connections: 16", "max_connections: 0"},
		{"streams", "max_concurrent_streams: 8", "max_concurrent_streams: 0"},
		{"headers", "max_header_list_size: 8192", "max_header_list_size: 0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yml")
			if err := os.WriteFile(path, []byte(strings.Replace(grpcConfig, test.before, test.after, 1)), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := settings.Load(path); err == nil {
				t.Fatal("unbounded configuration accepted")
			}
		})
	}
}

func TestCombinedShutdownBudgets(t *testing.T) {
	// Each budget fits alone, but Fx stops resources sequentially.
	contents := strings.Replace(validConfig, "shutdown_timeout: 10s", "shutdown_timeout: 6s", 1) + "  grpc:" + strings.Split(grpcConfig, "  grpc:")[1]
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.Load(path); err == nil {
		t.Fatal("combined shutdown budgets exceed the Node budget")
	}
}
