package preflight

import (
	"strings"
	"testing"
)

func TestRejectsAmbiguousOrActiveConfigWithoutEffects(t *testing.T) {
	const base = "listen: 127.0.0.1:443\ntrafficStats: {listen: '127.0.0.1:9999', secret: 'test-stats'}\nauth: {type: password, password: 'test-auth'}\n"
	for _, test := range []struct {
		name, src string
		issue     Issue
	}{
		{"trailing document", base + "---\nauth: {}\n", InvalidYAML},
		{"duplicate folded key", base + "Auth: {}\n", UnsupportedSource},
		{"alias", base + "other: &a 1\nthird: *a\n", UnsupportedSource},
		{"viper dotted key", base + "auth.type: command\n", UnsupportedSource},
		{"external stats", strings.Replace(base, "127.0.0.1:9999", "0.0.0.0:9999", 1), InvalidStats},
		{"zero stats port", strings.Replace(base, "127.0.0.1:9999", "127.0.0.1:0", 1), InvalidStats},
		{"range", strings.Replace(base, ":443", ":443,444", 1), InvalidListener},
		{"command", strings.Replace(base, "type: password", "type: command", 1), InvalidAuth},
		{"mimic", base + "mimic: {enabled: true}\n", UnsupportedSource},
		{"remote plaintext http auth", strings.Replace(base, "type: password, password: 'test-auth'", "type: http, http: {url: 'http://192.0.2.1/auth'}", 1), InvalidAuth},
		{"insecure HTTPS auth", strings.Replace(base, "type: password, password: 'test-auth'", "type: http, http: {url: 'https://example.test/auth', insecure: true}", 1), InvalidAuth},
		{"acme", base + "acme: {}\n", UnsupportedSource},
		{"tls missing", base, InvalidTLS},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, issue, err := (Source{}).Check(t.Context(), []byte(test.src))
			if issue != test.issue || err != nil {
				t.Fatalf("got issue %q with %v", issue, err)
			}
		})
	}
}
