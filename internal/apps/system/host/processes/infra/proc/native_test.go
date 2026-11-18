//go:build integration && linux

package proc

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/system/host/processes/domain"
)

func TestNativeProcessExitAndDescriptorCleanup(t *testing.T) {
	reader, err := New()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sleep", "60")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	pid := int32(command.Process.Pid)
	process, err := reader.Read(context.Background(), pid)
	if err != nil || process.PID != pid || process.ParentPID != int32(os.Getpid()) || process.StartTicks == 0 {
		t.Fatalf("%+v %v", process, err)
	}
	// Warm up the native runtime before comparing open descriptors.
	if _, err := reader.List(context.Background(), domain.Selection{AfterPID: pid - 1, Limit: 1}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		if _, err := reader.Count(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.Read(context.Background(), pid); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.List(context.Background(), domain.Selection{AfterPID: pid - 1, Limit: 1}); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("native reads leaked descriptors: %d -> %d", len(before), len(after))
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	if _, err := reader.Read(context.Background(), pid); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("exited process: %v", err)
	}
}
