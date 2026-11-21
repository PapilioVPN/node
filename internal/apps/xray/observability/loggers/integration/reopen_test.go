//go:build integration && linux

package integration_test

import (
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/testutil"
)

func TestLoggerReopeningMovesRealNativeLogsOverIndependentTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f := newFixture(t, pki, broker, transport)
			client := startNode(t, f)
			awaitNative(t, f.native)
			if loggerStatus(t, client, transport).LastOperation != nil {
				t.Fatal("new Node retained an operation journal")
			}
			exerciseReopening(t, f, client, transport)
		})
	}
}
func exerciseReopening(t *testing.T, f fixture, client clients, transport string) {
	t.Helper()
	state := loggerStatus(t, client, transport)
	if state.Generation != 1 || !state.APIConfigured || state.RequestInFlight || state.RequiresInstanceRestart {
		t.Fatalf("unexpected initial state %+v", state)
	}
	sendTraffic(t, f, transport+"-before-rotation")
	awaitLog(t, f.access, transport+"-before-rotation")
	if err := os.Rename(f.access, f.access+".rotated-"+transport); err != nil {
		t.Fatal(err)
	}
	sendTraffic(t, f, transport+"-still-old-writer")
	awaitLog(t, f.access+".rotated-"+transport, transport+"-still-old-writer")
	result, code := reopen(t, client, transport)
	if code != "" || result.Effect != "completed" || !result.Acknowledged || result.RequiresInstanceRestart || result.Generation != 1 || result.ObservedAt == nil || result.DurationNS < 0 {
		t.Fatalf("reopen=%+v code=%s", result, code)
	}
	sendTraffic(t, f, transport+"-new-writer")
	awaitLog(t, f.access, transport+"-new-writer")
	old, err := os.ReadFile(f.access + ".rotated-" + transport)
	if err != nil || strings.Contains(string(old), transport+"-new-writer") {
		t.Fatal("reopened traffic still reached rotated writer", err)
	}
	state = loggerStatus(t, client, transport)
	if state.LastOperation == nil || state.LastOperation.Effect != "completed" || !state.LastOperation.Acknowledged || state.Generation != 1 || state.RequiresInstanceRestart {
		t.Fatalf("acknowledgement lost in readback %+v", state)
	}
	// The native Start failure follows a successful Close, and its terminal error
	// does not prove rollback. Correct the private path and explicitly try again.
	if err := os.Rename(f.access, f.access+".second-"+transport); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.access, 0700); err != nil {
		t.Fatal(err)
	}
	failed, code := reopen(t, client, transport)
	if code != "unavailable" || failed.Effect != "unknown" || failed.Acknowledged || failed.RequiresInstanceRestart {
		t.Fatalf("settled native failure lost effects %+v %s", failed, code)
	}
	state = loggerStatus(t, client, transport)
	if state.LastOperation == nil || state.LastOperation.Effect != "unknown" || state.RequiresInstanceRestart {
		t.Fatal("settled error falsely locked native recovery")
	}
	if err := os.Remove(f.access); err != nil {
		t.Fatal(err)
	}
	repaired, code := reopen(t, client, transport)
	if code != "" || repaired.Effect != "completed" || !repaired.Acknowledged {
		t.Fatalf("explicit correction failed %+v %s", repaired, code)
	}
	sendTraffic(t, f, transport+"-after-correction")
	awaitLog(t, f.access, transport+"-after-correction")
}

func TestUnsupportedAndUnsentLoggerRequestsDoNotLockRecovery(t *testing.T) {
	for _, mode := range []string{"unconfigured", "unimplemented", "unreachable"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, testutil.NewPKI(t), "", "http")
			switch mode {
			case "unconfigured":
				f.config.Instances[0].Xray.API = nil
			case "unimplemented":
				path := f.config.Instances[0].Xray.Configuration
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var native map[string]any
				if err := json.Unmarshal(data, &native); err != nil {
					t.Fatal(err)
				}
				native["api"].(map[string]any)["services"] = []string{"StatsService"}
				data, _ = json.Marshal(native)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "unreachable":
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				f.config.Instances[0].Xray.API.Address = listener.Addr().String()
				listener.Close()
			}
			client := startNode(t, f)
			awaitNative(t, f.native)
			for range 2 {
				result, code := reopen(t, client, "http")
				expected := "failed_precondition"
				if mode == "unreachable" {
					expected = "unavailable"
				}
				if code != expected || result.Effect != "not_applied" || result.RequiresInstanceRestart || result.Acknowledged {
					t.Fatalf("%s %+v %s", mode, result, code)
				}
			}
			if loggerStatus(t, client, "http").RequiresInstanceRestart {
				t.Fatal("unsent or unsupported call poisoned the execution")
			}
		})
	}
}
