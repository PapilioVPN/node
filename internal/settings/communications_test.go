package settings_test

import (
	"errors"
	"github.com/PapilioVPN/node/internal/settings"
	"testing"
	"time"
)

func TestHTTPOnlyConfiguration(t *testing.T) {
	_, err := settings.Load(writeConfig(t, `node: {id: west}
runtime: {startup_timeout: 2s, shutdown_timeout: 2s}
communications:
  http:
    enabled: true
    listen: '127.0.0.1:0'
    request_timeout: 1s
    read_timeout: 2s
    write_timeout: 2s
    idle_timeout: 5s
    max_body_bytes: 1024
    max_header_bytes: 4096
    max_concurrent: 4
    max_connections: 8
    tls: {certificate_file: server.pem, private_key_file: key.pem, client_ca_file: ca.pem}
`))
	if err != nil {
		t.Fatalf("valid HTTP-only configuration rejected: %v", err)
	}
}

func TestHTTPRejectsUnboundedSettings(t *testing.T) {
	base := settings.Config{
		Node: settings.Node{ID: "west"}, Runtime: settings.Runtime{StartupTimeout: time.Second, ShutdownTimeout: time.Second},
		Communications: settings.Communications{HTTP: settings.HTTP{Enabled: true, Listen: "127.0.0.1:0", RequestTimeout: time.Second,
			ReadTimeout: time.Second, WriteTimeout: 2 * time.Second, IdleTimeout: time.Second, MaxBodyBytes: 1024, MaxHeaderBytes: 4096, MaxConcurrent: 1, MaxConnections: 8,
			TLS: settings.TLS{CertificateFile: "cert", PrivateKeyFile: "key", ClientCAFile: "ca"}}},
	}
	for name, mutate := range map[string]func(*settings.Config){
		"invalid port":    func(c *settings.Config) { c.Communications.HTTP.Listen = "localhost:70000" },
		"runtime budget":  func(c *settings.Config) { c.Runtime.StartupTimeout = 0 },
		"request timeout": func(c *settings.Config) { c.Communications.HTTP.RequestTimeout = 0 },
		"write budget":    func(c *settings.Config) { c.Communications.HTTP.WriteTimeout = time.Second },
		"body":            func(c *settings.Config) { c.Communications.HTTP.MaxBodyBytes = 0 },
		"headers":         func(c *settings.Config) { c.Communications.HTTP.MaxHeaderBytes = 0 },
		"concurrency":     func(c *settings.Config) { c.Communications.HTTP.MaxConcurrent = 0 },
		"trust roots":     func(c *settings.Config) { c.Communications.HTTP.TLS.ClientCAFile = "" },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := base
			mutate(&cfg)
			if err := cfg.Validate(); !errors.Is(err, settings.ErrInvalidConfig) {
				t.Fatalf("unbounded HTTP settings accepted: %v", err)
			}
		})
	}
}
