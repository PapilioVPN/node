package settings_test

import (
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/settings"
)

func TestRequestBoundsRespectTransportConfiguration(t *testing.T) {
	config := settings.Config{StartupTimeout: time.Second, ShutdownTimeout: 5 * time.Second, Communications: settings.Communications{Requests: settings.Requests{Timeout: time.Second}}}
	config.Communications.Requests.NATS = &settings.NATSRequests{SubjectPrefix: "fixture.node-a", PendingMessages: 8, PendingBytes: 8192, ShutdownTimeout: time.Second}
	if err := config.Validate(); err == nil {
		t.Fatal("NATS requests accepted without a connection")
	}
	// Load a valid native transport before exercising the common request bound.
	_, err := loadConfiguration(t, strings.Replace(validConfig, "timeout: 100ms", "timeout: 2s", 1))
	if err == nil {
		t.Fatal("request timeout equal to HTTP write_timeout was accepted")
	}
}

func TestNATSRequestPrefixAndCapacityValidation(t *testing.T) {
	input := strings.Replace(natsConfig, "communications:\n", "communications:\n"+commonNATSRequests, 1)
	_, err := loadConfiguration(t, input)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct{ old, replacement string }{
		{"fixture.node-a", "fixture.*"}, {"fixture.node-a", "fixture..node"},
		{"fixture.node-a", "fixture node"}, {"pending_messages: 8", "pending_messages: 0"},
		{"pending_bytes: 8192", "pending_bytes: -1"},
		{"shutdown_timeout: 1s", "shutdown_timeout: 0s"},
		{"shutdown_timeout: 1s", "shutdown_timeout: 99h"},
	} {
		if _, err := loadConfiguration(t, strings.Replace(input, test.old, test.replacement, 1)); err == nil {
			t.Fatalf("invalid request setting accepted: %q", test.replacement)
		}
	}
}

func TestPerCapabilityAccessConfigurationIsRejected(t *testing.T) {
	input := "startup_timeout: 1s\nshutdown_timeout: 5s\nsystem:\n  host:\n    identity:\n      allow_read: true\n"
	if _, err := loadConfiguration(t, input); err == nil {
		t.Fatal("obsolete per-capability access flag was silently accepted")
	}
}
