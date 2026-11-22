//go:build integration && linux

package integration_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/testutil"
	"golang.org/x/sys/unix"
)

func TestNormalNodeRestartCrashRecoveryLeasesAndTransportParity(t *testing.T) {
	binary := buildNode(t)
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	e := recoverableEngine(t, "managed")
	config := nodeConfig(t, pki, broker, "all", e)
	config.Communications.HTTP.Address = availableAddress(t)
	config.Communications.GRPC.Address = availableAddress(t)
	node, c := startExecutable(t, binary, pki, config)
	awaitReady(t, c, "http", e.settings.ID)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "instance-traffic-proof") }))
	defer target.Close()
	applyFixtureSources(t, c, e)
	targetURL, _ := url.Parse(target.URL)
	verifyTunnel, closeTunnel := retainedTunnel(t, e.proxy, targetURL.Host)
	defer closeTunnel()
	verifyTunnel()
	original := childRecord(t, e)
	if original.SourceDirectory == "" {
		t.Fatal("fixture did not stage actual sources")
	}
	// The private child record is only a fault-injection fixture; public status never exposes PID/token.
	fd, err := unix.PidfdOpen(original.PID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	defer unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
	for _, transport := range []string{"http", "grpc", "nats"} {
		inventory := requireExtended(t, c, transport, "list", "", nil)
		if len(inventory["items"].([]any)) != 1 {
			t.Fatal(inventory)
		}
		r := requireExtended(t, c, transport, "repair", e.settings.ID, map[string]any{"execution_id": original.ExecutionID, "max_attempts": uint32(3), "replace_unready": false})
		if r["ready"] != true || r["attempts"] != float64(0) || r["action"] != "attached" {
			t.Fatal(r)
		}
		if childRecord(t, e).PID != original.PID {
			t.Fatal("healthy repair restarted Xray")
		}
		if err := traffic(e.proxy, target.URL); err != nil {
			t.Fatal(err)
		}
	}
	requireExtended(t, c, "http", "retention", e.settings.ID, map[string]any{"execution_id": original.ExecutionID, "preserve": true})
	node.stop(t, syscall.SIGTERM)
	verifyTunnel()
	if err := traffic(e.proxy, target.URL); err != nil {
		t.Fatal("clean Node stop interrupted retained engine", err)
	}
	node, c = startExecutable(t, binary, pki, config)
	awaitReady(t, c, "grpc", e.settings.ID)
	verifyTunnel()
	after := childRecord(t, e)
	if after.PID != original.PID || after.ExecutionID != original.ExecutionID || after.Generation != original.Generation {
		t.Fatal("restart replaced healthy execution", after)
	}
	for _, transport := range []string{"http", "grpc", "nats"} {
		page := requireExtended(t, c, transport, "list", "", nil)
		status := page["items"].([]any)[0].(map[string]any)
		if status["reattached"] != true || status["identity_verified"] != true || status["configuration_revision"] != "1" {
			t.Fatal(status)
		}
	}
	// Fault injection models lost publication after fork: keep the durable intent
	// and real Xray, but remove the PID acknowledgement while Node is dead.
	node.stop(t, syscall.SIGKILL)
	injectPendingIntent(t, e)
	verifyTunnel()
	if err := traffic(e.proxy, target.URL); err != nil {
		t.Fatal(err)
	}
	node, c = startExecutable(t, binary, pki, config)
	awaitReady(t, c, "nats", e.settings.ID)
	verifyTunnel()
	if childRecord(t, e).ExecutionID != original.ExecutionID {
		t.Fatal("Node crash duplicated engine")
	}
	// Both owners die: the next ordinary startup cleans only the recorded source
	// directory, then creates one new execution. Unrelated work is preserved.
	closeTunnel()
	staleDirectory := original.SourceDirectory
	unrelated := filepath.Join(e.settings.Xray.WorkDirectory, "operator-owned")
	if err := os.WriteFile(unrelated, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	node.stop(t, syscall.SIGKILL)
	if err := unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0); err != nil {
		t.Fatal(err)
	}
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	if n, err := unix.Poll(poll, 2000); err != nil || n != 1 {
		t.Fatal("orphan exit not joined", err)
	}
	node, c = startExecutable(t, binary, pki, config)
	awaitReady(t, c, "http", e.settings.ID)
	recovered := childRecord(t, e)
	if recovered.ExecutionID == original.ExecutionID || recovered.Generation <= original.Generation {
		t.Fatal("dead orphan not replaced", recovered)
	}
	if _, err := os.Stat(staleDirectory); !os.IsNotExist(err) {
		t.Fatal("orphan source retained", err)
	}
	if data, err := os.ReadFile(unrelated); err != nil || string(data) != "keep" {
		t.Fatal("unrelated file changed", err)
	}
	original = recovered
	recoveredFD, err := unix.PidfdOpen(original.PID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(recoveredFD)
	defer unix.PidfdSendSignal(recoveredFD, unix.SIGKILL, nil, 0)
	keep := requireExtended(t, c, "http", "acquire", e.settings.ID, map[string]any{"expected_revision": "0", "execution_id": original.ExecutionID, "owner": "fixture", "ttl_seconds": uint32(1), "policy": "keep_running"})
	if keep["token"] == "" {
		t.Fatal("missing renewal token")
	}
	expiredKeep := awaitExpiredLease(t, c, "http", e.settings.ID)
	if childRecord(t, e).PID != original.PID || expiredKeep["stop_effect"] != "not_applied" {
		t.Fatal("keep_running expiry stopped engine", expiredKeep)
	}
	revision := expiredKeep["revision"].(string)
	for _, transport := range []string{"http", "grpc", "nats"} {
		receipt := requireExtended(t, c, transport, "acquire", e.settings.ID, map[string]any{"expected_revision": revision, "execution_id": original.ExecutionID, "owner": "fixture", "ttl_seconds": uint32(10), "policy": "keep_running"})
		token := receipt["token"].(string)
		status := receipt["status"].(map[string]any)
		renewed := requireExtended(t, c, transport, "renew", e.settings.ID, map[string]any{"expected_revision": status["revision"], "token": token, "ttl_seconds": uint32(10)})
		public := requireExtended(t, c, transport, "lease", e.settings.ID, nil)
		if strings.Contains(string(mustJSON(public)), token) || strings.Contains(string(mustJSON(public)), original.Token) {
			t.Fatal("lease status leaked token")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := c.extended(ctx, transport, "renew", e.settings.ID, map[string]any{"expected_revision": status["revision"], "token": token, "ttl_seconds": uint32(10)})
		cancel()
		if err == nil {
			t.Fatal("stale heartbeat renewed lease")
		}
		released := requireExtended(t, c, transport, "release", e.settings.ID, map[string]any{"expected_revision": renewed["revision"], "token": token})
		revision = released["revision"].(string)
	}
	// The lease belongs to an execution, so expiry cannot stop a manual replacement.
	requireExtended(t, c, "nats", "acquire", e.settings.ID, map[string]any{"expected_revision": revision, "execution_id": original.ExecutionID, "owner": "fixture", "ttl_seconds": uint32(1), "policy": "stop"})
	if _, err := c.control(context.Background(), "http", "restart", e.settings.ID); err != nil {
		t.Fatal(err)
	}
	original = childRecord(t, e)
	awaitReady(t, c, "http", e.settings.ID)
	newFD, err := unix.PidfdOpen(original.PID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(newFD)
	defer unix.PidfdSendSignal(newFD, unix.SIGKILL, nil, 0)
	stale := awaitExpiredLease(t, c, "grpc", e.settings.ID)
	if stale["expiry_error"] != "execution_changed" || stale["stop_effect"] != "not_applied" {
		t.Fatal("stale lease stopped replacement", stale)
	}
	if err := traffic(e.proxy, target.URL); err != nil {
		t.Fatal(err)
	}
	revision = stale["revision"].(string)
	// A stop lease survives Node death and is evaluated before the replacement Node serves requests.
	requireExtended(t, c, "nats", "acquire", e.settings.ID, map[string]any{"expected_revision": revision, "execution_id": original.ExecutionID, "owner": "fixture", "ttl_seconds": uint32(1), "policy": "stop"})
	node.stop(t, syscall.SIGKILL)
	time.Sleep(1100 * time.Millisecond)
	node, c = startExecutable(t, binary, pki, config)
	lease := requireExtended(t, c, "grpc", "lease", e.settings.ID, nil)
	if lease["state"] != "expired" || lease["stop_effect"] != "completed" {
		t.Fatal(lease)
	}
	observed, err := c.status(context.Background(), "http", e.settings.ID)
	if err != nil || observed.State != "stopped" {
		t.Fatal(observed, err)
	}
	// Bounded explicit repair starts a new execution after lease expiry.
	repaired := requireExtended(t, c, "grpc", "repair", e.settings.ID, map[string]any{"execution_id": original.ExecutionID, "max_attempts": uint32(1)})
	if repaired["ready"] != true || repaired["attempts"] != float64(1) {
		t.Fatal(repaired)
	}
	replacement := childRecord(t, e)
	if replacement.ExecutionID == original.ExecutionID || replacement.Generation <= original.Generation {
		t.Fatal(replacement)
	}
	replacementFD, err := unix.PidfdOpen(replacement.PID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(replacementFD)
	defer unix.PidfdSendSignal(replacementFD, unix.SIGKILL, nil, 0)
	// Real external crash is observed, then repaired through HTTP.
	if err := unix.PidfdSendSignal(replacementFD, unix.SIGKILL, nil, 0); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		observed, err = c.status(context.Background(), "nats", e.settings.ID)
		if err == nil && observed.State == "exited" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("crash not observed", observed, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	requireExtended(t, c, "http", "repair", e.settings.ID, map[string]any{"execution_id": replacement.ExecutionID, "max_attempts": uint32(1)})
	final := childRecord(t, e)
	finalFD, err := unix.PidfdOpen(final.PID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(finalFD)
	defer unix.PidfdSendSignal(finalFD, unix.SIGKILL, nil, 0)
	if err := traffic(e.proxy, target.URL); err != nil {
		t.Fatal(err)
	}
	node.stop(t, syscall.SIGTERM)
	if err := traffic(e.proxy, target.URL); err == nil {
		t.Fatal("default shutdown retained engine")
	}
	entries, err := os.ReadDir(e.settings.Xray.WorkDirectory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "execution-") {
			t.Fatal("orphaned source", filepath.Join(e.settings.Xray.WorkDirectory, entry.Name()))
		}
	}
}
