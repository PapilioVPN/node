package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionSourceConfigurationDefaultsAndExplicitPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("startup_timeout: 1s\nshutdown_timeout: 1s\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := Load(path)
	if err != nil || config.Sessions.BusSocketPath != "/run/dbus/system_bus_socket" || config.Sessions.UtmpPath != "/run/utmp" {
		t.Fatalf("%+v %v", config.Sessions, err)
	}
	input := "startup_timeout: 1s\nshutdown_timeout: 1s\nsessions:\n  bus_socket_path: /custom/bus\n  utmp_path: /custom/utmp\n"
	if err := os.WriteFile(path, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	config, err = Load(path)
	if err != nil || config.Sessions.BusSocketPath != "/custom/bus" || config.Sessions.UtmpPath != "/custom/utmp" {
		t.Fatalf("%+v %v", config.Sessions, err)
	}
	for _, sources := range []Sessions{{BusSocketPath: "relative"}, {UtmpPath: "relative"}, {BusSocketPath: "/" + strings.Repeat("a", 107)}, {UtmpPath: "/bad\x00path"}} {
		if err := sources.validate(); err == nil {
			t.Fatal("accepted invalid source path")
		}
	}
}
