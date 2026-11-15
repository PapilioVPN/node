package xray

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

func TestVersionOutputPreservesNativeFields(t *testing.T) {
	before := time.Now()
	info, err := parseVersionOutput([]byte("Xray 26.11.9 (Xray, Penetrates Everything.) Custom-dirty (go1.27.3 linux/arm64)\r\nA unified platform.\n"))
	if err != nil || info.Version != "26.11.9" || info.Build != "Custom-dirty" || info.GoVersion != "go1.27.3" || info.OperatingSystem != "linux" || info.Architecture != "arm64" || info.ObservedAt.Before(before) || info.ObservedAt.After(time.Now()) {
		t.Fatalf("native information changed: %+v, %v", info, err)
	}
	for _, invalid := range []string{"", "not Xray", "Xray 26.11.9", "Xray 26.11.9 (name) build (go1.27.3 linux)", "Xray 26.11.9 (name) build (go1.27.3 linux/amd64) trailing"} {
		if info, err := parseVersionOutput([]byte(invalid)); !errors.Is(err, domain.ErrInvalidInfoOutput) || info.Version != "" || !info.ObservedAt.IsZero() {
			t.Fatalf("unknown format became successful information: %+v, %v", info, err)
		}
	}
}

func TestNativeVersionOutputIsBoundedAcrossWrites(t *testing.T) {
	writer := versionOutput{limit: 4}
	if n, err := writer.Write([]byte("ab")); n != 2 || err != nil {
		t.Fatalf("first write: %d, %v", n, err)
	}
	if n, err := writer.Write([]byte("cd")); n != 2 || err != nil {
		t.Fatalf("exact limit: %d, %v", n, err)
	}
	if n, err := writer.Write([]byte(strings.Repeat("x", 10000))); n != 0 || !errors.Is(err, domain.ErrInfoOutputLimit) || !writer.exceeded || string(writer.data) != "abcd" {
		t.Fatalf("excess output retained or accepted: %d, %v, %d", n, err, len(writer.data))
	}
}

func FuzzVersionOutput(f *testing.F) {
	f.Add([]byte("Xray 26.11.9 (Xray, Penetrates Everything.) native-build (go1.27.3 linux/amd64)\n"))
	f.Add([]byte("Xray invalid\x00\r\n"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, output []byte) {
		// Native reads are bounded before parsing; keep fuzz work bounded as well.
		if len(output) > 4096 {
			t.Skip()
		}
		info, err := parseVersionOutput(output)
		if err != nil {
			if !errors.Is(err, domain.ErrInvalidInfoOutput) || info != (domain.BinaryInfo{}) {
				t.Fatalf("invalid output returned partial facts: %+v, %v", info, err)
			}
			return
		}
		if info.Version == "" || info.Build == "" || info.GoVersion == "" || info.OperatingSystem == "" || info.Architecture == "" || info.ObservedAt.IsZero() {
			t.Fatalf("accepted incomplete native statement: %+v", info)
		}
	})
}
