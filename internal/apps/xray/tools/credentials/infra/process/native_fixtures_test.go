//go:build integration && linux

package process

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func nativeRunner(t *testing.T) *Runner {
	t.Helper()
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Fatal("XRAY_TEST_BINARY must select the pinned executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	owner := NewExecutor()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if owner.Close(ctx) != nil {
			t.Error("owner failed to close")
		}
	})
	r, err := New(Config{Binary: binary, Directory: t.TempDir()}, owner)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
