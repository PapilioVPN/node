//go:build integration && linux

package logind

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/domain"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/godbus/dbus/v5"
)

type nativeManager struct {
	lists atomic.Int32
	gets  atomic.Int32
}

func (manager *nativeManager) ListSessionsEx() ([]extendedSession, *dbus.Error) {
	manager.lists.Add(1)
	return []extendedSession{{ID: "s1", UID: 42, User: "test", Path: "/org/freedesktop/login1/session/s1"}}, nil
}
func (manager *nativeManager) GetSession(id string) (dbus.ObjectPath, *dbus.Error) {
	manager.gets.Add(1)
	if id != "s1" {
		return "", &dbus.Error{Name: "org.freedesktop.login1.NoSuchSession"}
	}
	return "/org/freedesktop/login1/session/s1", nil
}

type nativeProperties struct{ calls atomic.Int32 }

func (properties *nativeProperties) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	properties.calls.Add(1)
	if iface != destination+".Session" {
		return nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownInterface"}
	}
	return map[string]dbus.Variant{"Id": dbus.MakeVariant("s1"), "Name": dbus.MakeVariant("test"), "User": dbus.MakeVariant(struct {
		UID  uint32
		Path dbus.ObjectPath
	}{42, "/org/freedesktop/login1/user/u42"}), "Seat": dbus.MakeVariant(struct {
		ID   string
		Path dbus.ObjectPath
	}{"", "/"}), "Timestamp": dbus.MakeVariant(uint64(9007199254740993)), "RemoteHost": dbus.MakeVariant("")}, nil
}
func TestPrivateBusBulkTargetedReadsAndDescriptorCleanup(t *testing.T) {
	bus := testutil.NewDBus(t)
	server, err := dbus.Connect(bus.Address)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if _, err := server.RequestName(destination, dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	manager, properties := &nativeManager{}, &nativeProperties{}
	if err := server.Export(manager, managerPath, destination+".Manager"); err != nil {
		t.Fatal(err)
	}
	if err := server.Export(properties, "/org/freedesktop/login1/session/s1", "org.freedesktop.DBus.Properties"); err != nil {
		t.Fatal(err)
	}
	reader := Reader{SocketPath: bus.SocketPath, Timeout: time.Second}
	page, err := reader.List(context.Background(), domain.Selection{Limit: 1})
	if err != nil || len(page.Sessions) != 1 || page.Sessions[0].UID != 42 || !page.Extended || manager.lists.Load() != 1 || manager.gets.Load() != 0 || properties.calls.Load() != 0 {
		t.Fatalf("%+v %v", page, err)
	}
	result, err := reader.Read(context.Background(), "s1")

	if err != nil || result.StartedUnixUS == nil || *result.StartedUnixUS != 9007199254740993 || result.RemoteHost == nil || *result.RemoteHost != "" || manager.gets.Load() != 1 || properties.calls.Load() != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	if _, err := reader.Read(context.Background(), "missing"); !errors.Is(err, domain.ErrNotFound) {
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
	}
	after, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("private D-Bus connection leak: %d -> %d", len(before), len(after))
	}
}
func TestAuthenticationDeadlineClosesNativeSocket(t *testing.T) {
	directory, err := os.MkdirTemp("", "node-dbus-stall-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "bus")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		io.Copy(io.Discard, connection)
	}()
	started := time.Now()
	_, err = (Reader{SocketPath: path, Timeout: 30 * time.Millisecond}).List(context.Background(), domain.Selection{Limit: 1})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("unbounded authentication: %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled authentication left a live peer connection")
	}
}
