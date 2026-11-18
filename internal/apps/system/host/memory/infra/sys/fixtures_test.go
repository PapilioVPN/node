package sys

import (
	"os"
	"path/filepath"
	"testing"
)

const meminfo = `MemTotal: 1024 kB
MemAvailable: 256 kB
MemFree: 128 kB
Active: 64 kB
Inactive: 32 kB
Buffers: 16 kB
Cached: 128 kB
SReclaimable: 32 kB
Shmem: 8 kB
Slab: 48 kB
`

func procFixture(t *testing.T, input string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "meminfo"), []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

type readerFunc func([]byte) (int, error)

func (read readerFunc) Read(p []byte) (int, error) { return read(p) }
