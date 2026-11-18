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

func cpuInfoFixture(t *testing.T, input string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cpuinfo"), []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

type readerFunc func([]byte) (int, error)

func (read readerFunc) Read(p []byte) (int, error) { return read(p) }

type repeatingLine struct {
	line   string
	offset int
}

func (source *repeatingLine) Read(p []byte) (int, error) {
	n := copy(p, source.line[source.offset:])
	source.offset += n
	if source.offset == len(source.line) {
		source.offset = 0
	}
	return n, nil
}

func frequencyFixture(t *testing.T, policies map[string]map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, attributes := range policies {
		path := filepath.Join(root, name)
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		for name, value := range attributes {
			if err := os.WriteFile(filepath.Join(path, name), []byte(value), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}
