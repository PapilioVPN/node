package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	instancesv1 "github.com/PapilioVPN/node/internal/engines/xray/instances/bridge/grpc/proto/v1"
	handlercmd "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/blackhole"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func TestLiveUpdatePreservesTunnelAndRestartReplacesExecution(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.RemoteAddr)
	}))
	defer origin.Close()

	ports := availablePorts(t, 2)
	before := proxyConfig(ports[0], "freedom", "original")
	f := newFixture(t, before)
	start, err := f.client.Start(f.ctx, &instancesv1.XrayInstanceRequest{Id: "primary"})
	if err != nil || start.GetStartAction() != instancesv1.XrayStartAction_XRAY_START_ACTION_STARTED {
		t.Fatalf("initial Start = (%v, %v)", start, err)
	}
	originalPID := f.childPID()
	f.expectRule("original")
	persistent := proxyClient(t, ports[0])
	session, err := fetchOrigin(f.ctx, persistent, origin.URL)
	if err != nil || session == "" {
		t.Fatalf("initial proxy flow = (%q, %v)", session, err)
	}

	after := proxyConfig(ports[0], "blackhole", "updated")
	f.startHTTP(`{"config":`+after+`}`, "live_updated")
	f.expectRule("updated")
	f.startHTTP(`{"config":`+after+`}`, "heartbeat")
	if pid := f.childPID(); pid != originalPID {
		t.Fatalf("live update changed Xray PID from %d to %d", originalPID, pid)
	}
	if got, err := fetchOrigin(f.ctx, persistent, origin.URL); err != nil || got != session {
		t.Fatalf("established proxy flow = (%q, %v), want %q", got, err, session)
	}
	if got, err := fetchOrigin(f.ctx, proxyClient(t, ports[0]), origin.URL); err == nil {
		t.Fatalf("new connection passed through the changed outbound: %q", got)
	}

	replacement := proxyConfig(ports[1], "freedom", "replacement")
	start, err = f.client.Start(f.ctx, &instancesv1.XrayInstanceRequest{Id: "primary", ConfigJson: []byte(replacement)})
	if err != nil || start.GetStartAction() != instancesv1.XrayStartAction_XRAY_START_ACTION_RESTARTED {
		t.Fatalf("incompatible Start = (%v, %v)", start, err)
	}
	if pid := f.childPID(); pid == originalPID {
		t.Fatalf("incompatible config kept PID %d", pid)
	}
	if got, err := fetchOrigin(f.ctx, persistent, origin.URL); err == nil {
		t.Fatalf("old proxy port still served a request: %q", got)
	}
	if got, err := fetchOrigin(f.ctx, proxyClient(t, ports[1]), origin.URL); err != nil || got == "" {
		t.Fatalf("replacement proxy flow = (%q, %v)", got, err)
	}
}

func TestInvalidCandidateKeepsReadyExecution(t *testing.T) {
	f := newFixture(t, `{"log":{"loglevel":"none"}}`)
	f.startHTTP("", "started")
	previous := f.childPID()
	_, err := f.client.Start(f.ctx, &instancesv1.XrayInstanceRequest{Id: "primary", ConfigJson: []byte(
		`{"outbounds":[{"tag":"bad","protocol":"unknown-test-protocol"}]}`)})
	f.expectStartFailure(err, codes.InvalidArgument,
		instancesv1.XrayStartFailureReason_XRAY_START_FAILURE_REASON_CONFIG_REJECTED,
		instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_READY)
	if pid := f.childPID(); pid != previous {
		t.Fatalf("rejected candidate replaced PID %d with %d", previous, pid)
	}
	f.startHTTP("", "heartbeat")
}

func TestRejectedLiveStepReplacesPartiallyUpdatedExecution(t *testing.T) {
	f := newFixture(t, `{"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[]}}`)
	f.startHTTP("", "started")
	previous := f.childPID()

	// Introduce native drift so the first AddOutbound succeeds but the next
	// fails with an existing tag. The public path must recover from that state.
	paths, err := filepath.Glob(filepath.Join(f.dir, "*", "api.sock"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("native sockets = (%v, %v)", paths, err)
	}
	address := url.URL{Scheme: "unix", Path: paths[0]}
	conn, err := grpc.NewClient(address.String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = handlercmd.NewHandlerServiceClient(conn).AddOutbound(f.ctx, &handlercmd.AddOutboundRequest{
		Outbound: &core.OutboundHandlerConfig{Tag: "collision", ProxySettings: serial.ToTypedMessage(&blackhole.Config{})},
	})
	if err != nil {
		t.Fatal(err)
	}

	f.startHTTP(`{"config":{"outbounds":[{"tag":"direct","protocol":"freedom"},
		{"tag":"new","protocol":"blackhole"},{"tag":"collision","protocol":"blackhole"}],
		"routing":{"rules":[{"ruleTag":"candidate","network":"tcp","outboundTag":"new"}]}}}`, "restarted_after_live_failure")
	if pid := f.childPID(); pid == previous {
		t.Fatalf("fallback retained partially updated PID %d", pid)
	}
	f.expectRule("candidate")
}

func TestRestartFailureReportsObservedState(t *testing.T) {
	f := newFixture(t, `{"log":{"loglevel":"none"}}`)
	f.startHTTP("", "started")
	address, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer address.Close()
	_, err = f.client.Start(f.ctx, &instancesv1.XrayInstanceRequest{
		Id: "primary", ConfigJson: []byte(proxyConfig(address.Addr().(*net.TCPAddr).Port, "freedom", "occupied")),
	})
	failure := f.expectStartFailure(err, codes.Unavailable,
		instancesv1.XrayStartFailureReason_XRAY_START_FAILURE_REASON_EXITED_BEFORE_READY,
		instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_FAILED)
	if failure.GetStatus().GetStartAction() != instancesv1.XrayStartAction_XRAY_START_ACTION_RESTARTED {
		t.Fatalf("restart failure = %v", failure)
	}
	f.expectHTTP(http.MethodGet, "", "failed")
	f.expectClean()
}

func (f fixture) expectStartFailure(err error, code codes.Code, reason instancesv1.XrayStartFailureReason,
	state instancesv1.XrayInstanceState) *instancesv1.XrayStartFailure {
	f.t.Helper()
	if status.Code(err) != code {
		f.t.Fatalf("Start = %v, want %v", err, code)
	}
	details := status.Convert(err).Details()
	if len(details) != 1 {
		f.t.Fatalf("Start details = %v, want a typed failure", details)
	}
	failure, ok := details[0].(*instancesv1.XrayStartFailure)
	if !ok || failure.GetReason() != reason || failure.GetStatus().GetState() != state {
		f.t.Fatalf("Start details = %v, want %v and %v", details, reason, state)
	}
	return failure
}

func (f fixture) expectRule(tag string) {
	f.t.Helper()
	res, err := f.http.Client().Get(f.http.URL + "/v1/xray/instances/primary/routing/rules")
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	var got struct {
		Rules []struct {
			Tag string `json:"rule_tag"`
		}
	}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil || res.StatusCode != http.StatusOK ||
		len(got.Rules) != 1 || got.Rules[0].Tag != tag {
		f.t.Fatalf("routing rules = (%+v, %s, %v), want %s", got, res.Status, err, tag)
	}
}

func proxyConfig(port int, protocol, ruleTag string) string {
	const local = `"settings":{"finalRules":[{"action":"allow","ip":["127.0.0.1/32"]}]}`
	settings := ""
	if protocol == "freedom" {
		settings = "," + local
	}
	return fmt.Sprintf(`{
		"log":{"loglevel":"none"},
		"inbounds":[{"tag":"fixture","listen":"127.0.0.1","port":%d,"protocol":"socks"}],
		"outbounds":[{"tag":"direct","protocol":"freedom",%s},{"tag":"switch","protocol":%q%s}],
		"routing":{"rules":[{"ruleTag":%q,"inboundTag":["fixture"],"outboundTag":"switch"}]}
	}`, port, local, protocol, settings, ruleTag)
}

func proxyClient(t *testing.T, port int) *http.Client {
	t.Helper()
	proxy := &url.URL{Scheme: "socks5", Host: fmt.Sprintf("127.0.0.1:%d", port)}
	transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: time.Second}
}

func fetchOrigin(ctx context.Context, client *http.Client, target string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	return strings.TrimSpace(string(body)), err
}
