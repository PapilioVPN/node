package sys

import (
	"os"
	"path/filepath"
	"testing"
)

func procStat(t *testing.T, contents string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "stat"), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}
