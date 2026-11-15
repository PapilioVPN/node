package settings_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/settings"
)

func TestNATSOnlyConfiguration(t *testing.T) {
	cfg, err := settings.Load(writeConfig(t, `node: {id: west}
runtime: {startup_timeout: 2s, shutdown_timeout: 2s}
communications:
  nats:
    enabled: true
    url: 'tls://localhost:4222'
    subject_prefix: papilio.west.v1
    queue_group: west
    request_timeout: 1s
    connect_timeout: 1s
    drain_timeout: 1s
    max_request_bytes: 1024
    max_response_bytes: 65536
    max_pending_messages: 8
    max_pending_bytes: 8192
    tls: {certificate_file: node.pem, private_key_file: node-key.pem, ca_file: broker-ca.pem}
`))
	if err != nil {
		t.Fatalf("valid NATS-only configuration rejected: %v", err)
	}
	for _, path := range []string{cfg.Communications.NATS.TLS.CertificateFile, cfg.Communications.NATS.TLS.PrivateKeyFile, cfg.Communications.NATS.TLS.CAFile} {
		if !filepath.IsAbs(path) || filepath.Dir(path) != filepath.Dir(cfg.Communications.NATS.TLS.CertificateFile) {
			t.Fatalf("NATS trust paths not resolved against settings: %s", path)
		}
	}
}

func TestNATSRejectsInvalidTrustAndBounds(t *testing.T) {
	base := settings.Config{Node: settings.Node{ID: "west"}, Runtime: settings.Runtime{StartupTimeout: time.Second, ShutdownTimeout: time.Second}, Communications: settings.Communications{NATS: settings.NATS{
		Enabled: true, URL: "tls://localhost:4222", SubjectPrefix: "nodes.west.v1", QueueGroup: "west", RequestTimeout: time.Second, ConnectTimeout: time.Second, DrainTimeout: time.Second,
		MaxRequestBytes: 128, MaxResponseBytes: 1024, MaxPendingMessages: 2, MaxPendingBytes: 512,
		TLS: settings.NATSClientTLS{CertificateFile: "client", PrivateKeyFile: "key", CAFile: "broker"},
	}}}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*settings.Config){
		"plaintext":        func(c *settings.Config) { c.Communications.NATS.URL = "nats://localhost:4222" },
		"zero port":        func(c *settings.Config) { c.Communications.NATS.URL = "tls://localhost:000" },
		"credentials":      func(c *settings.Config) { c.Communications.NATS.URL = "tls://private:secret@localhost:4222" },
		"invalid URL":      func(c *settings.Config) { c.Communications.NATS.URL = "%" },
		"wildcard prefix":  func(c *settings.Config) { c.Communications.NATS.SubjectPrefix = "nodes.>" },
		"empty token":      func(c *settings.Config) { c.Communications.NATS.SubjectPrefix = "nodes..v1" },
		"queue whitespace": func(c *settings.Config) { c.Communications.NATS.QueueGroup = "node west" },
		"request timeout":  func(c *settings.Config) { c.Communications.NATS.RequestTimeout = 0 },
		"connect timeout":  func(c *settings.Config) { c.Communications.NATS.ConnectTimeout = 0 },
		"drain timeout":    func(c *settings.Config) { c.Communications.NATS.DrainTimeout = 0 },
		"request bytes":    func(c *settings.Config) { c.Communications.NATS.MaxRequestBytes = 0 },
		"response bytes":   func(c *settings.Config) { c.Communications.NATS.MaxResponseBytes = 1 },
		"pending messages": func(c *settings.Config) { c.Communications.NATS.MaxPendingMessages = 0 },
		"pending bytes":    func(c *settings.Config) { c.Communications.NATS.MaxPendingBytes = 1 },
		"broker roots":     func(c *settings.Config) { c.Communications.NATS.TLS.CAFile = "" },
		"runtime":          func(c *settings.Config) { c.Runtime.StartupTimeout = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := base
			mutate(&cfg)
			err := cfg.Validate()
			if !errors.Is(err, settings.ErrInvalidConfig) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("invalid NATS settings accepted or exposed: %v", err)
			}
		})
	}
}
