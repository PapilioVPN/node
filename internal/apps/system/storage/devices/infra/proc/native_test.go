//go:build integration && linux

package proc

import (
	"context"
	"os"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/domain"
)

func TestNativeDiskstatsReadsReleaseDescriptors(t *testing.T) {
	reader, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.List(context.Background(), domain.Selection{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		if _, err := reader.List(context.Background(), domain.Selection{Limit: 1}); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("native device descriptor leak: %d -> %d", len(before), len(after))
	}
}
