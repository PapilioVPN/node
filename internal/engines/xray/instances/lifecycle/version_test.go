package lifecycle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVersionProbeReusesOnlyTheUnchangedExecutable(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "xray")
	count := filepath.Join(dir, "probes")
	t.Setenv("NODE_TEST_VERSION_PROBES", count)
	install := func(version string) {
		t.Helper()
		next := filepath.Join(dir, "next")
		script := "#!/bin/sh\nprintf x >> \"$NODE_TEST_VERSION_PROBES\"\nprintf 'Xray " + version + " (Xray)\n'\n"
		if err := os.WriteFile(next, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(next, executable); err != nil {
			t.Fatal(err)
		}
	}
	var launcher processLauncher
	read := func(want string) {
		t.Helper()
		identity, err := os.Stat(executable)
		if err != nil {
			t.Fatal(err)
		}
		if got := launcher.readVersion(t.Context(), executable, identity); got != want {
			t.Fatalf("version = %q, want %q", got, want)
		}
	}
	install("26.11.9")
	read("26.11.9")
	read("26.11.9")
	install("26.11.8")
	read("26.11.8")
	read("26.11.8")
	install("invalid")
	read("")
	read("")
	probes, err := os.ReadFile(count)
	if err != nil || len(probes) != 4 {
		t.Fatalf("recorded %d version probes, want 4 (%v)", len(probes), err)
	}
}
