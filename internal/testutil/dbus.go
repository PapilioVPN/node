package testutil

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type DBus struct {
	SocketPath string
	Address    string
}

// NewDBus starts one isolated native daemon; capability fakes belong to their
// own test suites. The production runtime has no dependency on this executable.
func NewDBus(t testing.TB) DBus {
	t.Helper()
	directory, err := os.MkdirTemp("", "node-dbus-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	path := filepath.Join(directory, "bus")
	// Keep the isolated test bus independent of desktop policies and activation.
	// AppArmor mediation belongs to a real host bus, not this owned fake service.
	config := filepath.Join(directory, "bus.conf")
	contents := fmt.Sprintf(`<busconfig>
 <type>session</type>
 <listen>unix:path=%s</listen>
 <auth>EXTERNAL</auth>
 <apparmor mode="disabled"/>
 <policy context="default">
 <allow user="%d"/>
 <allow own="*"/>
 <allow send_destination="*"/>
 <allow receive_sender="*"/>
 </policy>
 </busconfig>`, path, os.Getuid())
	if err := os.WriteFile(config, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("dbus-daemon", "--config-file="+config, "--nofork", "--print-address=1", "--address=unix:path="+path)
	command.Stderr = io.Discard
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		stdout.Close()
		t.Fatalf("start isolated dbus-daemon (native test dependency): %v", err)
	}
	exited, readDone := make(chan struct{}), make(chan struct{})
	go func() { command.Wait(); close(exited) }()
	ready := make(chan string, 1)
	go func() {
		defer close(readDone)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1024), 4096)
		if scanner.Scan() {
			ready <- scanner.Text()
		} else {
			ready <- ""
		}
	}()
	t.Cleanup(func() {
		command.Process.Kill()
		stdout.Close()
		select {
		case <-exited:
		case <-time.After(3 * time.Second):
			t.Error("isolated D-Bus process did not exit")
		}
		select {
		case <-readDone:
		case <-time.After(time.Second):
			t.Error("isolated D-Bus readiness reader did not exit")
		}
	})
	select {
	case address := <-ready:
		if !strings.HasPrefix(address, "unix:") {
			t.Fatal("isolated D-Bus did not provide its address")
		}
		return DBus{SocketPath: path, Address: address}
	case <-exited:
		t.Fatal("isolated D-Bus exited before readiness")
	case <-time.After(3 * time.Second):
		t.Fatal("isolated D-Bus readiness timed out")
	}
	return DBus{}
}
