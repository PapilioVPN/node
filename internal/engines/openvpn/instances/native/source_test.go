package native

import (
	"strings"
	"testing"
)

func TestManagedSource(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want string
	}{
		{"server profile", "# managed fixture\n--dev tun\nserver 192.0.2.0 255.255.255.0\n<ca>\ncertificate bytes\n</ca>\n", ""},
		{"quoted directive", "\"management\" 0.0.0.0 9999", "unsupported_directive"},
		{"include", "config extra.conf", "unsupported_directive"},
		{"own management socket", "--management /tmp/foreign unix", "unsupported_directive"},
		{"plugin", "plugin helper.so", "unsupported_directive"},
		{"accepts unauthenticated clients", "verify-client-cert none", "unsupported_directive"},
		{"inline close with suffix", "<ca>\n</ca> extra\nlog /tmp/foreign\n</ca>", "invalid_source"},
		{"unterminated inline block", "<ca>\ncert", "invalid_source"},
		{"native line bound", "push " + strings.Repeat("x", 260), "invalid_source"},
		{"empty", "", "invalid_source"},
		{"only comments", "# empty profile\n", "invalid_source"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := checkSource([]byte(tc.text))
			if got.Code != tc.want {
				t.Fatalf("issue = %+v, want %q", got, tc.want)
			}
		})
	}
}
