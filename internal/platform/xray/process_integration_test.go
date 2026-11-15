//go:build integration && linux

package xray_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/modules/instances/app"
	"github.com/PapilioVPN/node/internal/modules/instances/domain"
	"github.com/PapilioVPN/node/internal/platform/xray"
)

func TestIndependentXrayInstances(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "isolated-target")
	}))
	defer target.Close()
	first, firstAddress := realXray(t)
	second, secondAddress := realXray(t)
	firstService := app.NewLifecycle(domain.Instance{ID: "edge-a", Kind: domain.Xray}, first)
	secondService := app.NewLifecycle(domain.Instance{ID: "edge-b", Kind: domain.Xray}, second)
	startContext, cancelStart := context.WithCancel(t.Context())
	if _, err := firstService.Start(startContext); err != nil {
		t.Fatal(err)
	}
	cancelStart() // An accepted execution belongs to the owner, not its request.
	if _, err := secondService.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	firstClient, secondClient := proxyClient(t, firstAddress), proxyClient(t, secondAddress)
	awaitProxy(t, firstClient, target.URL)
	awaitProxy(t, secondClient, target.URL)

	var calls sync.WaitGroup
	for range 8 {
		calls.Go(func() {
			if _, err := firstService.Start(t.Context()); err != nil {
				t.Errorf("concurrent idempotent start: %v", err)
			}
		})
	}
	calls.Wait()
	status, err := firstService.Stop(t.Context())
	if err != nil || status.Instance.ID != "edge-a" || status.State != domain.Stopped {
		t.Fatalf("stop = %+v, %v", status, err)
	}
	assertReleased(t, firstAddress)
	awaitProxy(t, secondClient, target.URL)
	if status, err := secondService.Status(t.Context()); err != nil || status.State != domain.Running {
		t.Fatalf("other instance affected: %+v, %v", status, err)
	}
	if _, err := firstService.Stop(t.Context()); err != nil {
		t.Fatalf("repeated stop: %v", err)
	}
	if _, err := firstService.Start(t.Context()); err != nil {
		t.Fatalf("restart: %v", err)
	}
	awaitProxy(t, firstClient, target.URL)
	if _, err := firstService.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := secondService.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertReleased(t, firstAddress)
	assertReleased(t, secondAddress)
}

func TestXrayConfigurationFailureIsObserved(t *testing.T) {
	binary := xrayBinary(t)
	configFile := filepath.Join(t.TempDir(), "xray.json")
	if err := os.WriteFile(configFile, []byte(`{"inbounds":`), 0o600); err != nil {
		t.Fatal(err)
	}
	process := ownedProcess(t, xray.Config{Binary: binary, ConfigFile: configFile, StopTimeout: time.Second})
	if err := process.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	await(t, func() bool { return processState(process) == domain.Exited })
	var exit *exec.ExitError
	state, failure := process.Observe()
	if state != domain.Exited || !errors.As(failure, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("native configuration observation was lost: %s, %v", state, failure)
	}
	if err := process.Stop(t.Context()); !errors.As(err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("native configuration exit was lost: %v", err)
	}
}

func TestXrayAdmissionAndMissingExecutable(t *testing.T) {
	directory := t.TempDir()
	process := ownedProcess(t, xray.Config{
		Binary:     filepath.Join(directory, "missing-xray"),
		ConfigFile: filepath.Join(directory, "xray.json"), StopTimeout: time.Second,
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := process.Start(ctx); !errors.Is(err, context.Canceled) || processState(process) != domain.Stopped {
		t.Fatalf("canceled admission = %v, state = %s", err, processState(process))
	}
	if err := process.Start(t.Context()); !errors.Is(err, os.ErrNotExist) || processState(process) != domain.Stopped {
		t.Fatalf("missing executable = %v, state = %s", err, processState(process))
	}
}

// This controlled OS child verifies termination behavior, not Xray functionality.
func TestStopDeadlineKillsAndJoinsUncooperativeChild(t *testing.T) {
	directory := t.TempDir()
	binary, config := filepath.Join(directory, "child"), filepath.Join(directory, "config.json")
	script := "#!/bin/sh\ntrap '' TERM\nprintf '%s' \"$$\" > \"$3.pid\"\nexec /bin/sleep 60\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	process := ownedProcess(t, xray.Config{Binary: binary, ConfigFile: config, StopTimeout: 2 * time.Second})
	if err := process.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	var pid int
	await(t, func() bool {
		data, err := os.ReadFile(config + ".pid")
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(string(data))
		return err == nil
	})
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- process.Stop(ctx) }()
	await(t, func() bool { return processState(process) == domain.Stopping })
	admission, cancelAdmission := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancelAdmission()
	if err := process.Start(admission); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("mutation admission did not cancel while stopping: %v", err)
	}
	if err := <-stopped; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stop deadline was lost: %v", err)
	}
	if processState(process) != domain.Stopped {
		t.Fatalf("child was not joined: %s", processState(process))
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("owned child still exists after Stop: %v", err)
	}
}

func xrayBinary(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Fatal("XRAY_TEST_BINARY is required; run make integration")
	}
	return binary
}

func ownedProcess(t *testing.T, config xray.Config) *xray.Process {
	t.Helper()
	process, err := xray.New(config)
	if err != nil {
		t.Fatal(err)
	}
	// Tests inspecting an expected exit/deadline already assert its native error.
	// Always perform final cleanup even if a test fails before reaching that assertion.
	t.Cleanup(func() { _ = process.Close() })
	return process
}

func realXray(t *testing.T) (*xray.Process, string) {
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
	return ownedProcess(t, xray.Config{Binary: xrayBinary(t), ConfigFile: path, StopTimeout: 2 * time.Second}), address
}

func proxyClient(t *testing.T, address string) *http.Client {
	t.Helper()
	proxy, err := url.Parse("http://" + address)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 200 * time.Millisecond}
}

func awaitProxy(t *testing.T, client *http.Client, target string) {
	t.Helper()
	await(t, func() bool {
		response, err := client.Get(target)
		if err != nil {
			return false
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, 64))
		return err == nil && response.StatusCode == http.StatusOK && string(body) == "isolated-target"
	})
}

func assertReleased(t *testing.T, address string) {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("listener leaked after stop: %v", err)
	}
	listener.Close()
}

func await(t *testing.T, ready func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !ready() {
		select {
		case <-ctx.Done():
			t.Fatalf("native observation did not complete: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func processState(process *xray.Process) domain.State {
	state, _ := process.Observe()
	return state
}
