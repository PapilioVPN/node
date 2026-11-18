//go:build integration && linux

package sys_test

import (
	"os"
	"strings"
	"testing"
	"time"
)

func procUptime(t *testing.T) time.Duration {
	t.Helper()
	contents, err := os.ReadFile("/proc/uptime")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(contents))
	if len(fields) != 2 {
		t.Fatalf("unexpected /proc/uptime: %q", contents)
	}
	value, err := time.ParseDuration(fields[0] + "s")
	if err != nil {
		t.Fatal(err)
	}
	return value
}
