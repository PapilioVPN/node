//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/testutil"
	"golang.org/x/sys/unix"
)

func TestNativeFileIOContinuesAfterTimeoutAndRequiresNewExecution(t *testing.T) {
	f := newFixture(t, testutil.NewPKI(t), "", "http")
	f.config.Communications.Requests.Timeout = 150 * time.Millisecond
	client := startNode(t, f)
	awaitNative(t, f.native)
	first, code := reopen(t, client, "http")
	if code != "" || !first.Acknowledged {
		t.Fatalf("native warmup failed %+v %s", first, code)
	}
	sendTraffic(t, f, "before-fifo")
	awaitLog(t, f.access, "before-fifo")
	if err := os.Rename(f.access, f.access+".before-fifo"); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(f.access, 0600); err != nil {
		t.Fatal(err)
	}
	result, code := reopen(t, client, "http")
	if code != "deadline_exceeded" || result.Effect != "unknown" || !result.RequiresInstanceRestart || result.Acknowledged {
		t.Fatalf("remote file I/O cancellation was falsely claimed %+v %s", result, code)
	}
	state := loggerStatus(t, client, "http")
	if !state.RequiresInstanceRestart || state.RequestInFlight || state.LastOperation == nil || state.LastOperation.Effect != "unknown" {
		t.Fatalf("unresolved call lost in readback %+v", state)
	}
	again, code := reopen(t, client, "http")
	if code != "restart_required" || again.Effect != "not_applied" || !again.RequiresInstanceRestart {
		t.Fatalf("unresolved generation admitted another remote mutation %+v %s", again, code)
	}
	// A reader releases the actual native O_WRONLY FIFO open. Its handler ignores
	// the expired RPC context and completes later; no fake native server is used.
	reader, err := unix.Open(f.access, unix.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(reader)
	if err := os.Remove(f.access); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.access, nil, 0600); err != nil {
		t.Fatal(err)
	}
	sendTraffic(t, f, "native-completed-after-timeout")
	awaitLog(t, f.access, "native-completed-after-timeout")
	if !loggerStatus(t, client, "http").RequiresInstanceRestart {
		t.Fatal("unobserved late native completion silently cleared uncertainty")
	}
	restartInstance(t, client)
	state = loggerStatus(t, client, "http")
	if state.Generation != 2 || state.RequiresInstanceRestart || state.LastOperation == nil || state.LastOperation.Generation != 1 {
		t.Fatalf("execution replacement did not resolve recovery boundary %+v", state)
	}
	result, code = reopen(t, client, "http")
	if code != "" || result.Generation != 2 || !result.Acknowledged || result.Effect != "completed" {
		t.Fatalf("new execution remained fenced %+v %s", result, code)
	}
}
func restartInstance(t *testing.T, c clients) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "POST", c.address+"/v1/core/engines/instances/edge-a/executions", nil)
	response, err := c.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("instance restart status=%d", response.StatusCode)
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, _ := http.NewRequestWithContext(ctx, "GET", c.address+"/v1/core/engines/instances/edge-a/readiness", nil)
		response, err := c.http.Do(request)
		if err == nil {
			var result struct {
				APIState string `json:"api_state"`
			}
			decodeErr := json.NewDecoder(response.Body).Decode(&result)
			response.Body.Close()
			if decodeErr == nil && result.APIState == "ready" {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("replaced execution's shared native channel did not recover")
		case <-ticker.C:
		}
	}
}
