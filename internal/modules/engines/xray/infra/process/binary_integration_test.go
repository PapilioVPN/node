//go:build integration && linux

package process_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	xrayprocess "github.com/PapilioVPN/node/internal/modules/engines/xray/infra/process"
)

func TestBinaryInspectorReadsOfficialXray(t *testing.T) {
	reader := binaryInspector(t, xrayBinary(t), time.Second, 1024)
	before := time.Now()
	info, err := reader.ReadBinaryInfo(t.Context())
	if err != nil || info.Version != "26.11.9" || info.Build == "" || !strings.HasPrefix(info.GoVersion, "go") || info.OperatingSystem != "linux" || info.Architecture != "amd64" || info.ObservedAt.Before(before) || info.ObservedAt.After(time.Now()) {
		t.Fatalf("official binary information: %+v, %v", info, err)
	}
}

func TestBinaryInspectionErrorsAndOutputBounds(t *testing.T) {
	for _, test := range []struct {
		name, script string
		cause        error
	}{
		{"unsupported format", "printf 'private wrong executable output'", domain.ErrInvalidInfoOutput},
		{"bounded stdout", "printf '%010000d' 0", domain.ErrInfoOutputLimit},
		{"native exit", "printf 'private diagnostic' >&2\nexit 43", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			binary := controlledBinary(t, test.script)
			info, err := binaryInspector(t, binary, time.Second, 128).ReadBinaryInfo(t.Context())
			if test.cause != nil {
				if !errors.Is(err, test.cause) {
					t.Fatalf("failure cause lost: %v", err)
				}
			} else {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 43 {
					t.Fatalf("native exit cause lost: %v", err)
				}
			}
			if info.Version != "" || !info.ObservedAt.IsZero() || strings.Contains(err.Error(), "private") {
				t.Fatalf("failed execution became information or leaked diagnostics: %+v, %v", info, err)
			}
		})
	}
	reader := binaryInspector(t, filepath.Join(t.TempDir(), "missing"), time.Second, 128)
	if _, err := reader.ReadBinaryInfo(t.Context()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing binary cause: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := reader.ReadBinaryInfo(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled admission: %v", err)
	}
}

// Controlled OS children establish join/admission guarantees, not Xray behavior.
func TestBinaryInspectionCancellationAndSaturation(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(strconv.FormatBool(deadline), func(t *testing.T) {
			binary := controlledBinary(t, "printf '%s' \"$$\" > \"$0.pid\"\nexec /bin/sleep 60")
			timeout := 5 * time.Second
			if deadline {
				timeout = time.Second
			}
			reader := binaryInspector(t, binary, timeout, 128)
			ctx, cancel := context.WithCancel(t.Context())
			finished := make(chan struct{})
			var executionError error
			go func() { _, executionError = reader.ReadBinaryInfo(ctx); close(finished) }()
			defer func() { cancel(); <-finished }()
			var pid int
			await(t, func() bool {
				data, err := os.ReadFile(binary + ".pid")
				if err != nil {
					return false
				}
				pid, err = strconv.Atoi(string(data))
				return err == nil
			})
			if _, err := reader.ReadBinaryInfo(t.Context()); !errors.Is(err, domain.ErrInspectionBusy) {
				t.Fatalf("saturation queued another child: %v", err)
			}
			want := context.DeadlineExceeded
			if !deadline {
				cancel()
				want = context.Canceled
			}
			<-finished
			if !errors.Is(executionError, want) {
				t.Fatalf("termination cause lost: %v", executionError)
			}
			if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatalf("child not joined before return: %v", err)
			}
			if err := os.Remove(binary); err != nil {
				t.Fatal(err)
			}
			if _, err := reader.ReadBinaryInfo(t.Context()); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("admission leaked after termination: %v", err)
			}
		})
	}
}

func TestBinaryInspectionUsesNativeCommandWithNoInheritedEnvironment(t *testing.T) {
	t.Setenv("BINARY_INFO_TEST_OVERRIDE", "must-not-inherit")
	binary := controlledBinary(t, `test "$#" -eq 1 && test "$1" = version || exit 41
test -z "$BINARY_INFO_TEST_OVERRIDE" || exit 42
printf 'Xray 26.11.9 (test fixture) fixture (go1.27.3 linux/amd64)\n'`)
	if info, err := binaryInspector(t, binary, time.Second, 256).ReadBinaryInfo(t.Context()); err != nil || info.Build != "fixture" {
		t.Fatalf("native invocation/environment contract: %+v, %v", info, err)
	}
}

func binaryInspector(t *testing.T, binary string, timeout time.Duration, limit int) *xrayprocess.BinaryInspector {
	t.Helper()
	reader, err := xrayprocess.NewBinaryInspector(xrayprocess.BinaryInspectionConfig{Binary: binary, Timeout: timeout, MaxOutputBytes: limit, MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	return reader
}

func controlledBinary(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "controlled-binary")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
