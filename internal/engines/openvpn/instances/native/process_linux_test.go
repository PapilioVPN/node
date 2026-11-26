package native

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCanceledStartReapsChildAndRemovesRuntimeFiles(t *testing.T) {
	root, err := os.MkdirTemp("", "node-ovpn-unit-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	runtimeDir := filepath.Join(root, "runtime")
	if err := os.Mkdir(runtimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "engine")
	// This child stays alive but never connects to the management socket.
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf '%s\\n' \"$$\" > child.pid\nexec sleep 60\n"), 0700); err != nil {
		t.Fatal(err)
	}
	d := Driver{Executable: executable, ConfigFile: filepath.Join(root, "server.conf"), RuntimeDirectory: runtimeDir}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := d.Start(ctx, []byte("dev tun\n"))
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("process owner did not join")
		}
	})
	var raw []byte
	for ctx.Err() == nil {
		raw, err = os.ReadFile(filepath.Join(root, "child.pid"))
		if err == nil && len(raw) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal("fixture did not report its PID")
	}
	cancel()
	select {
	case err := <-done:
		close(done)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Start = %v, want cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not finish after cancellation")
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("owned child was not reaped: %v", err)
	}
	if files, err := os.ReadDir(runtimeDir); err != nil || len(files) != 0 {
		t.Fatalf("cancellation left runtime files: %v, %v", files, err)
	}
}
