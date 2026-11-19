//go:build integration && linux && amd64

package integration_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/godbus/dbus/v5"
)

type sourceSummary struct {
	ID            string
	UID           uint32
	User          string
	Seat          string
	Leader        uint32
	Class         string
	TTY           string
	Idle          bool
	IdleMonotonic uint64
	Path          dbus.ObjectPath
}
type sessionManager struct{}

func (sessionManager) ListSessionsEx() ([]sourceSummary, *dbus.Error) {
	return []sourceSummary{{ID: "s1", UID: 42, User: "fixture-user", Class: "user", Path: "/org/freedesktop/login1/session/s1"}}, nil
}
func (sessionManager) GetSession(id string) (dbus.ObjectPath, *dbus.Error) {
	if id != "s1" {
		return "", &dbus.Error{Name: "org.freedesktop.login1.NoSuchSession"}
	}
	return "/org/freedesktop/login1/session/s1", nil
}

type sessionProperties struct{}

func (sessionProperties) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	if iface != "org.freedesktop.login1.Session" {
		return nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownInterface"}
	}
	return map[string]dbus.Variant{"Id": dbus.MakeVariant("s1"), "Name": dbus.MakeVariant("fixture-user"), "User": dbus.MakeVariant(struct {
		UID  uint32
		Path dbus.ObjectPath
	}{42, "/org/freedesktop/login1/user/u42"}), "Seat": dbus.MakeVariant(struct {
		ID   string
		Path dbus.ObjectPath
	}{"", "/"}), "Timestamp": dbus.MakeVariant(uint64(9007199254740993)), "Remote": dbus.MakeVariant(false), "RemoteHost": dbus.MakeVariant("")}, nil
}
func sessionSources(t *testing.T) settings.Sessions {
	t.Helper()
	bus := testutil.NewDBus(t)
	server, err := dbus.Connect(bus.Address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	if _, err := server.RequestName("org.freedesktop.login1", dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	if err := server.Export(sessionManager{}, "/org/freedesktop/login1", "org.freedesktop.login1.Manager"); err != nil {
		t.Fatal(err)
	}
	if err := server.Export(sessionProperties{}, "/org/freedesktop/login1/session/s1", "org.freedesktop.DBus.Properties"); err != nil {
		t.Fatal(err)
	}
	record := make([]byte, 384)
	binary.LittleEndian.PutUint16(record, 7)
	binary.LittleEndian.PutUint32(record[4:8], 42)
	copy(record[8:40], "pts/1")
	copy(record[40:44], "p1")
	copy(record[44:76], "fixture-user")
	copy(record[76:332], "example.test")
	binary.LittleEndian.PutUint32(record[340:344], 123)
	binary.LittleEndian.PutUint32(record[344:348], 456789)
	path := filepath.Join(t.TempDir(), "utmp")
	if err := os.WriteFile(path, append(record, record...), 0600); err != nil {
		t.Fatal(err)
	}
	return settings.Sessions{BusSocketPath: bus.SocketPath, UtmpPath: path}
}
