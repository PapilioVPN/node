//go:build integration && linux

package process_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	controlprocess "github.com/PapilioVPN/node/internal/modules/core/instances/infra/xray/process"
	xrayprocess "github.com/PapilioVPN/node/internal/modules/xray/configs/infra/process"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestNativeConfigBuildingWithoutRuntimeOrFileMutation(t *testing.T) {
	active, address := realXray(t)
	if err := active.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	// The builder must not start listeners, even with an already occupied port.
	_, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	directory, work := t.TempDir(), t.TempDir()
	marker := filepath.Join(directory, "must-not-be-created.log")
	protected := filepath.Join(directory, "protected-config.json")
	const original = "operator-owned config must stay unchanged\n"
	if err := os.WriteFile(protected, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	validator := newValidator(t, xrayprocess.ValidationConfig{
		Binary: testutil.XrayBinary(t), Directory: directory, WorkDir: work,
		Timeout: 2 * time.Second, MaxBytes: 8192, MaxConcurrent: 1,
	})
	for _, test := range []struct {
		name    string
		format  domain.ConfigFormat
		content string
		valid   bool
	}{
		{"json including native comments", domain.JSON, fmt.Sprintf(`{
// Preserve Xray's native parser semantics.
"log":{"loglevel":"debug","error":%q,"access":%q},
"inbounds":[{"listen":"127.0.0.1","port":%s,"protocol":"http"}],
"outbounds":[{"protocol":"freedom"}]}`, protected, marker, portText), true},
		{"yaml", domain.YAML, "outbounds:\n  - protocol: freedom\n", true},
		{"toml", domain.TOML, "[[outbounds]]\nprotocol = 'freedom'\n", true},
		{"invalid syntax", domain.JSON, `{"inbounds":`, false},
		{"invalid native protocol", domain.JSON, `{"outbounds":[{"protocol":"not-an-xray-protocol"}]}`, false},
		{"invalid native port", domain.JSON, `{"inbounds":[{"listen":"127.0.0.1","protocol":"http"}]}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := validator.ValidateConfig(t.Context(), domain.ConfigCandidate{Format: test.format, Content: []byte(test.content)})
			if err != nil || result.Valid != test.valid {
				t.Fatalf("native build = %+v, %v; want valid=%v", result, err, test.valid)
			}
			if !test.valid {
				var exit *exec.ExitError
				if !errors.As(result.Failure, &exit) || exit.ExitCode() != 1 {
					t.Fatalf("native rejection cause lost: %v", result.Failure)
				}
			}
			assertEmptyDirectory(t, work)
		})
	}
	contents, err := os.ReadFile(protected)
	if err != nil || string(contents) != original {
		t.Fatalf("configuration was mutated: %q, %v", contents, err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate logger was initialized: %v", err)
	}
	if state, failure := active.Observe(); state != domain.Running || failure != nil {
		t.Fatalf("active execution changed: %s, %v", state, failure)
	}
}

func TestValidationBoundsAndExecutionFailure(t *testing.T) {
	directory, work := t.TempDir(), t.TempDir()
	validator := newValidator(t, xrayprocess.ValidationConfig{
		Binary: filepath.Join(directory, "missing"), Directory: directory, WorkDir: work,
		Timeout: time.Second, MaxBytes: 2, MaxConcurrent: 1,
	})
	for _, test := range []struct {
		candidate domain.ConfigCandidate
		cause     error
	}{
		{domain.ConfigCandidate{Format: domain.JSON}, domain.ErrInvalidInput},
		{domain.ConfigCandidate{Format: "../../bad", Content: []byte("{}")}, domain.ErrInvalidInput},
		{domain.ConfigCandidate{Format: domain.JSON, Content: []byte("123")}, domain.ErrConfigTooLarge},
		{domain.ConfigCandidate{Format: domain.JSON, Content: []byte("{}")}, os.ErrNotExist},
	} {
		result, err := validator.ValidateConfig(t.Context(), test.candidate)
		if !errors.Is(err, test.cause) || result.Valid || result.Failure != nil {
			t.Fatalf("boundary/build failure = %+v, %v; want %v", result, err, test.cause)
		}
		assertEmptyDirectory(t, work)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := validator.ValidateConfig(ctx, domain.ConfigCandidate{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled input admission = %v", err)
	}
}

// This controlled OS child establishes process bounds/cleanup, not Xray behavior.
func TestValidationImmediateSaturationAndCancellationJoin(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprintf("deadline=%v", deadline), func(t *testing.T) {
			directory, work := t.TempDir(), t.TempDir()
			binary := filepath.Join(directory, "controlled-validator")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s' \"$$\" > \"$5.pid\"\nexec /bin/sleep 60\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			timeout := 5 * time.Second
			if deadline {
				timeout = 200 * time.Millisecond
			}
			validator := newValidator(t, xrayprocess.ValidationConfig{
				Binary: binary, Directory: directory, WorkDir: work,
				Timeout: timeout, MaxBytes: 2, MaxConcurrent: 1,
			})
			candidate := domain.ConfigCandidate{Format: domain.JSON, Content: []byte("{}")}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			finished := make(chan error, 1)
			go func() { _, err := validator.ValidateConfig(ctx, candidate); finished <- err }()
			var pid int
			testutil.Await(t, func() bool {
				paths, err := filepath.Glob(filepath.Join(work, "*", "*.pid"))
				if err != nil || len(paths) != 1 {
					return false
				}
				data, err := os.ReadFile(paths[0])
				if err != nil {
					return false
				}
				pid, err = strconv.Atoi(string(data))
				return err == nil
			})
			if _, err := validator.ValidateConfig(t.Context(), candidate); !errors.Is(err, domain.ErrValidationBusy) {
				t.Fatalf("saturation did not reject immediately: %v", err)
			}
			want := context.DeadlineExceeded
			if !deadline {
				cancel()
				want = context.Canceled
			}
			if err := <-finished; !errors.Is(err, want) {
				t.Fatalf("cancellation cause lost: %v", err)
			}
			if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatalf("validation returned before joining child: %v", err)
			}
			assertEmptyDirectory(t, work)
			// The admitted slot is released on failure; the next call reaches exec.
			if err := os.Remove(binary); err != nil {
				t.Fatal(err)
			}
			if _, err := validator.ValidateConfig(t.Context(), candidate); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("slot was leaked: %v", err)
			}
		})
	}
}

func TestValidationDoesNotInheritHostOverrides(t *testing.T) {
	t.Setenv("XRAY_LOCATION_CONFDIR", "/a/private/invalid/host/path")
	t.Setenv("XRAY_JSON_STRICT", "true")
	validator := newValidator(t, xrayprocess.ValidationConfig{
		Binary: testutil.XrayBinary(t), Directory: t.TempDir(), WorkDir: t.TempDir(),
		Timeout: time.Second, MaxBytes: 128, MaxConcurrent: 1,
	})
	result, err := validator.ValidateConfig(t.Context(), domain.ConfigCandidate{Format: domain.JSON, Content: []byte("{// native comment\n}")})
	if err != nil || !result.Valid {
		t.Fatalf("host overrides reached native builder: %+v, %v", result, err)
	}
}

func newValidator(t *testing.T, config xrayprocess.ValidationConfig) *xrayprocess.Validator {
	t.Helper()
	validator, err := xrayprocess.NewValidator(config)
	if err != nil {
		t.Fatal(err)
	}
	return validator
}

func assertEmptyDirectory(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("candidate workspace leaked: %v, %v", entries, err)
	}
}

func ownedProcess(t *testing.T, config controlprocess.Config) *controlprocess.Process {
	t.Helper()
	process, err := controlprocess.New(config)
	if err != nil {
		t.Fatal(err)
	}
	// Tests inspecting an expected exit/deadline already assert its native error.
	// Always perform final cleanup even if a test fails before reaching that assertion.
	t.Cleanup(func() { _ = process.Close() })
	return process
}

func realXray(t *testing.T) (*controlprocess.Process, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`{"log":{"loglevel":"none"},"inbounds":[{"listen":"127.0.0.1","port":%d,"protocol":"http","settings":{}}],"outbounds":[{"protocol":"freedom"}]}`, port)
	path := filepath.Join(t.TempDir(), "xray.json")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return ownedProcess(t, controlprocess.Config{Binary: testutil.XrayBinary(t), ConfigFile: path, StopTimeout: 2 * time.Second}), address
}
