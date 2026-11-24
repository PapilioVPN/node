package integration_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	instancesv1 "github.com/PapilioVPN/node/internal/engines/xray/instances/bridge/grpc/proto/v1"
	"google.golang.org/grpc/codes"
)

func TestAppliedConfigSHA256(t *testing.T) {
	f := newFixture(t, `{"log":{"loglevel":"none"}}`)
	f.expectAppliedConfig("stopped", "")

	port := availablePorts(t, 1)[0]
	source := " \n" + proxyConfig(port, "freedom", "original") + "\t\n"
	req := &instancesv1.XrayInstanceRequest{Id: "primary", ConfigJson: []byte(source)}
	want := configDigest(source)
	start := f.startGRPC(req, instancesv1.XrayStartAction_XRAY_START_ACTION_STARTED)
	expectGRPCConfigDigest(t, start, want)
	f.expectAppliedConfig("ready", want)
	pid := f.childPID()

	heartbeat := f.startGRPC(req, instancesv1.XrayStartAction_XRAY_START_ACTION_HEARTBEAT)
	expectGRPCConfigDigest(t, heartbeat, want)
	f.expectAppliedConfig("ready", want)

	update := proxyConfig(port, "blackhole", "updated")
	want = configDigest(update)
	updated := f.startHTTP(`{"config":`+update+`}`, "live_updated")
	expectHTTPConfigDigest(t, updated, want)
	f.expectRule("updated")
	f.expectAppliedConfig("ready", want)

	const rejected = `{"outbounds":[{"tag":"bad","protocol":"unknown-test-protocol"}]}`
	_, err := f.client.Start(f.ctx, &instancesv1.XrayInstanceRequest{Id: "primary", ConfigJson: []byte(rejected)})
	failure := f.expectStartFailure(err, codes.InvalidArgument,
		instancesv1.XrayStartFailureReason_XRAY_START_FAILURE_REASON_CONFIG_REJECTED,
		instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_READY)
	expectGRPCConfigDigest(t, failure.GetStatus(), want)

	request, err := http.NewRequestWithContext(f.ctx, http.MethodPut,
		f.http.URL+"/v1/xray/instances/primary/execution", strings.NewReader(`{"config":`+rejected+`}`))
	if err != nil {
		t.Fatal(err)
	}
	response, err := f.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var rejectedStart struct {
		Reason string          `json:"reason"`
		Status executionStatus `json:"status"`
	}
	if err := json.NewDecoder(response.Body).Decode(&rejectedStart); err != nil ||
		response.StatusCode != http.StatusBadRequest || rejectedStart.Reason != "config_rejected" ||
		rejectedStart.Status.State != "ready" {
		t.Fatalf("HTTP start failure = (%+v, %s, %v)", rejectedStart, response.Status, err)
	}
	expectHTTPConfigDigest(t, rejectedStart.Status, want)
	f.expectAppliedConfig("ready", want)
	if got := f.childPID(); got != pid {
		t.Fatalf("live update or rejection changed PID from %d to %d", pid, got)
	}

	stopped := f.expectHTTP(http.MethodDelete, "/execution", "stopped")
	expectHTTPConfigDigest(t, stopped, "")
	f.expectAppliedConfig("stopped", "")
	f.expectClean()
}

func configDigest(source string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(source)))
}

func (f fixture) expectAppliedConfig(state, want string) {
	f.t.Helper()
	result, err := f.client.GetStatus(f.ctx, &instancesv1.XrayInstanceRequest{Id: "primary"})
	if err != nil {
		f.t.Fatal(err)
	}
	expectGRPCConfigDigest(f.t, result, want)
	resultHTTP := f.expectHTTP(http.MethodGet, "", state)
	expectHTTPConfigDigest(f.t, resultHTTP, want)
}

func expectGRPCConfigDigest(t *testing.T, result *instancesv1.XrayInstanceStatus, want string) {
	t.Helper()
	if want == "" {
		if result.AppliedConfigSha256 != nil {
			t.Fatalf("gRPC status returned an unconfirmed digest: %v", result)
		}
		return
	}
	if result.AppliedConfigSha256 == nil || result.GetAppliedConfigSha256() != want {
		t.Fatalf("gRPC status = %v, want applied config digest %s", result, want)
	}
}

func expectHTTPConfigDigest(t *testing.T, result executionStatus, want string) {
	t.Helper()
	if want == "" {
		if result.AppliedConfigSHA256 != nil {
			t.Fatalf("HTTP status returned an unconfirmed digest: %+v", result)
		}
		return
	}
	var digest string
	if err := json.Unmarshal(result.AppliedConfigSHA256, &digest); err != nil || digest != want {
		t.Fatalf("HTTP status = %+v, want applied config digest %s", result, want)
	}
}
