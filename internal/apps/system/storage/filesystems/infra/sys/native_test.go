//go:build integration && linux

package sys

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/domain"
)

func TestNativeFilesystemReadsReleaseDescriptorsAndReportMissingPaths(t *testing.T) {
	reader, err := New()
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir()
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
		if _, err := reader.Usage(context.Background(), path); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("native descriptor leak: %d -> %d", len(before), len(after))
	}
	if _, err := reader.Usage(context.Background(), filepath.Join(path, "missing")); !errors.Is(err, domain.ErrNotFound) || !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
