package policies

import (
	"strings"
	"testing"
)

func TestProjectionNeverPublishesNativeDetails(t *testing.T) {
	for _, raw := range []string{
		"[Info] proxy/vless/inbound: accepted tcp:private.example:443 email:secret@example.test password=private",
		"[Warning] password private-key eyJhbGciOi aGVsbG8= /private/config.json 192.0.2.1",
		"[Error] key=secret\x00\x1b[0m",
		"Xray 26.11.9 started with UUID 6a4bd380-73ec-4a6b-97ca-bafc8d8827a6",
		string([]byte{0xff, 0xfe}),
	} {
		level, kind, message := Project([]byte(raw))
		if strings.Contains(message, "private") || strings.Contains(message, "secret") || strings.Contains(message, "192.0") || len(message) > 100 || level == "" || kind == "" {
			t.Fatal(level, kind, message)
		}
	}
	level, kind, message := Project([]byte("2026/11/22 [Warning] core: Xray 26.11.9 started"))
	if level != "warning" || kind != "lifecycle" || message != "Xray started; details redacted" {
		t.Fatal(level, kind, message)
	}
}
