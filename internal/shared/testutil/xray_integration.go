//go:build integration

package testutil

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func XrayBinary(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Fatal("XRAY_TEST_BINARY is required; run make integration")
	}
	return binary
}
func Await(t *testing.T, ready func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !ready() {
		select {
		case <-ctx.Done():
			t.Fatalf("native observation did not complete: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}
func ControlledBinary(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "controlled-binary")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
