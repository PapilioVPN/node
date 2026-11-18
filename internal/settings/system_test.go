package settings_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/settings"
)

func TestHostIdentityRequiresBoundedExplicitConfiguration(t *testing.T) {
	base := func() settings.Config {
		return settings.Config{StartupTimeout: time.Second, ShutdownTimeout: 5 * time.Second,
			System: settings.System{Host: settings.Host{Identity: &settings.HostIdentity{RequestTimeout: time.Second}}}}
	}
	for _, test := range []struct {
		name   string
		mutate func(*settings.Config)
	}{
		{"zero timeout", func(c *settings.Config) { c.System.Host.Identity.RequestTimeout = 0 }},
		{"missing broker", func(c *settings.Config) {
			c.System.Host.Identity.NATS = &settings.HostIdentityNATS{Subject: "node.a", PendingMessages: 1, PendingBytes: 1024}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := base()
			test.mutate(&config)
			if err := config.Validate(); err == nil {
				t.Fatal("invalid capability configuration accepted")
			}
		})
	}
	config := base()
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	if config.System.Host.Identity.AllowRead {
		t.Fatal("read permission must be explicit")
	}
}

func TestHostIdentityNATSSelectionAndCapacityValidation(t *testing.T) {
	const capability = `system:
  host:
    identity:
      request_timeout: 100ms
      nats:
        subject: fixture.node-a.identity
        pending_messages: 8
        pending_bytes: 8192
        shutdown_timeout: 1s
`
	for _, test := range []struct{ old, replacement string }{
		{"fixture.node-a.identity", "fixture.*.identity"},
		{"fixture.node-a.identity", "fixture..identity"},
		{"fixture.node-a.identity", "fixture node-a"},
		{"pending_messages: 8", "pending_messages: 0"},
		{"pending_bytes: 8192", "pending_bytes: -1"},
		{"shutdown_timeout: 1s", "shutdown_timeout: 0s"},
		{"shutdown_timeout: 1s", "shutdown_timeout: 99h"},
	} {
		path := filepath.Join(t.TempDir(), "config.yml")
		input := natsConfig + strings.Replace(capability, test.old, test.replacement, 1)
		if err := os.WriteFile(path, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := settings.Load(path); err == nil {
			t.Fatalf("invalid setting accepted: %q", test.replacement)
		}
	}
}
