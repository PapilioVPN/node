package testutil

import (
	"os"
	"testing"
)

// NewPrivateDirectory creates a temporary directory restricted to its owner.
func NewPrivateDirectory(t testing.TB) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	return directory
}
