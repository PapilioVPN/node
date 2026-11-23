package integration_test

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	instancesv1 "github.com/PapilioVPN/node/internal/engines/xray/instances/bridge/grpc/proto/v1"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type executionStatus struct {
	State  string `json:"state"`
	Action string `json:"start_action"`
}

func TestStartConfigurationAndHeartbeat(t *testing.T) {
	f := newFixture(t, `{"log":{"loglevel":"none"}}`)
	const body = `{"config":{"log":{"loglevel":"error"}},"exclude_inbounds":["manual"],"keep_alive_seconds":10}`
	f.startHTTP(body, "started")
	f.startHTTP(body, "heartbeat")
	f.startHTTP(`{"config":{"log":{"loglevel":"none"}}}`, "restarted")
	f.expectHTTP(http.MethodDelete, "/execution", "stopped")
	f.expectClean()
}

func (f fixture) startHTTP(body, wantAction string) {
	f.t.Helper()
	req, err := http.NewRequestWithContext(f.ctx, http.MethodPut, f.http.URL+"/v1/xray/instances/primary/execution", strings.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.http.Client().Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		f.t.Fatalf("start HTTP = %s", resp.Status)
	}
	var result executionStatus
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.State != "ready" || result.Action != wantAction {
		f.t.Fatalf("start HTTP = (%+v, %v), want ready %s", result, err, wantAction)
	}
}

func TestKeepAliveRefreshAndExpiry(t *testing.T) {
	f := newFixture(t, `{"log":{"loglevel":"none"}}`)
	req := &instancesv1.XrayInstanceRequest{Id: "primary", KeepAliveSeconds: new(uint32(1))}
	first, err := f.client.Start(f.ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	second, err := f.client.Start(f.ctx, &instancesv1.XrayInstanceRequest{Id: "primary"})
	if err != nil || second.GetStartAction() != instancesv1.XrayStartAction_XRAY_START_ACTION_HEARTBEAT ||
		second.GetKeepAliveExpiresAt() == nil || !second.GetKeepAliveExpiresAt().AsTime().After(first.GetKeepAliveExpiresAt().AsTime()) {
		t.Fatalf("heartbeat = (%v, %v), first start = %v", second, err, first)
	}

	deadline := second.GetKeepAliveExpiresAt().AsTime()
	for {
		current, err := f.client.GetStatus(f.ctx, &instancesv1.XrayInstanceRequest{Id: "primary"})
		if err != nil {
			t.Fatal(err)
		}
		switch current.GetState() {
		case instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_STOPPED:
			if time.Now().Before(deadline) || current.GetKeepAliveExpiresAt() != nil {
				t.Fatalf("stopped before lease expiry or reported a stale lease: %v", current)
			}
			f.expectClean()
			return
		case instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_READY:
			if !current.GetKeepAliveExpiresAt().AsTime().Equal(deadline) {
				t.Fatalf("GetStatus changed the lease: %v", current)
			}
		}
		if time.Now().After(deadline.Add(2 * time.Second)) {
			t.Fatalf("Xray after lease expiry = %v", current)
		}
		time.Sleep(40 * time.Millisecond)
	}
}

func TestInitialUsersAndExcludedInbounds(t *testing.T) {
	ports := availablePorts(t, 2)
	source := fmt.Sprintf(`{"log":{"loglevel":"none"},"inbounds":[
		{"tag":"managed","listen":"127.0.0.1","port":%d,"protocol":"vless","settings":{"decryption":"none","clients":[]}},
		{"tag":"manual","listen":"127.0.0.1","port":%d,"protocol":"vless","settings":{"decryption":"none","clients":[
			{"id":"4fd87324-c5f4-473f-85dc-53518d5fe814","email":"local"}]}}]}`, ports[0], ports[1])
	f := newFixture(t, source)
	req := &instancesv1.XrayInstanceRequest{Id: "primary", ConfigJson: []byte(source), ExcludeInbounds: []string{"manual"},
		InitialUsers: []*instancesv1.XrayInitialInboundUsers{
			{InboundTag: "managed", Accounts: []*instancesv1.XrayInitialAccount{
				{Email: "initial", Uuid: "d5120cd4-15b3-4ca7-a198-0a32d269c135"},
			}},
			{InboundTag: "manual"}, // This clear must be skipped.
		},
	}
	start, err := f.client.Start(f.ctx, req)
	if err != nil || start.GetStartAction() != instancesv1.XrayStartAction_XRAY_START_ACTION_STARTED {
		t.Fatalf("start with users = (%v, %v)", start, err)
	}
	f.expectHTTPCount("managed", 1)
	f.expectHTTPCount("manual", 1)
	req.InitialUsers[0].Accounts = nil
	start, err = f.client.Start(f.ctx, req)
	if err != nil || start.GetStartAction() != instancesv1.XrayStartAction_XRAY_START_ACTION_HEARTBEAT {
		t.Fatalf("repeated start with changed users = (%v, %v)", start, err)
	}
	f.expectHTTPCount("managed", 1)
	f.expectHTTP(http.MethodDelete, "/execution", "stopped")

	req.InitialUsers = []*instancesv1.XrayInitialInboundUsers{{InboundTag: "missing"}}
	start, err = f.client.Start(f.ctx, req)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("seed failure = (%v, %v)", start, err)
	}
	details := status.Convert(err).Details()
	if len(details) != 1 {
		t.Fatalf("seed failure details = %v", details)
	}
	failure, ok := details[0].(*instancesv1.XrayStartFailure)
	if !ok || failure.GetReason() != instancesv1.XrayStartFailureReason_XRAY_START_FAILURE_REASON_INITIAL_ACCOUNTS_FAILED ||
		failure.GetStatus().GetState() != instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_FAILED {
		t.Fatalf("seed failure details = %v", details)
	}
	f.expectClean()
}

func (f fixture) expectHTTPCount(tag string, want int) {
	f.t.Helper()
	resp, err := f.http.Client().Get(f.http.URL + "/v1/xray/instances/primary/inbounds/" + tag + "/accounts/count")
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct{ Count int }
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil || resp.StatusCode != http.StatusOK || got.Count != want {
		f.t.Fatalf("account count for %s = (%d, %s, %v), want %d", tag, got.Count, resp.Status, err, want)
	}
}

func TestStartRecoversAnUnresponsiveExecution(t *testing.T) {
	f := newFixture(t, `{"log":{"loglevel":"none"}}`)
	f.startHTTP("", "started")
	old := f.childPID()
	if err := unix.Kill(old, unix.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	f.startHTTP("", "recovered")
	if newPID := f.childPID(); newPID == old {
		t.Fatalf("recovery retained the unresponsive PID %d", old)
	}
}

func (f fixture) childPID() int {
	f.t.Helper()
	paths, err := filepath.Glob(filepath.Join(f.dir, "*", "api.sock"))
	if err != nil || len(paths) != 1 {
		f.t.Fatalf("Xray runtime sockets = (%v, %v)", paths, err)
	}
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: paths[0], Net: "unix"})
	if err != nil {
		f.t.Fatal(err)
	}
	defer conn.Close()
	raw, err := conn.SyscallConn()
	if err != nil {
		f.t.Fatal(err)
	}
	var credentials *unix.Ucred
	var peerErr error
	err = raw.Control(func(fd uintptr) {
		credentials, peerErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if err != nil || peerErr != nil || credentials.Pid <= 0 {
		f.t.Fatalf("Xray peer credentials = (%v, %v, %v)", credentials, err, peerErr)
	}
	return int(credentials.Pid)
}

func availablePorts(t *testing.T, count int) []int {
	t.Helper()
	var ports []int
	for range count {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		ports = append(ports, listener.Addr().(*net.TCPAddr).Port)
	}
	return ports
}
