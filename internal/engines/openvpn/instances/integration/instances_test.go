package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	pb "github.com/PapilioVPN/node/internal/engines/openvpn/instances/bridge/grpc/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
)

func TestNativeLifecycle(t *testing.T) {
	f := newFixture(t)
	conn, err := grpc.NewClient(f.grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	instances := pb.NewOpenVPNInstancesServiceClient(conn)
	configs := pb.NewOpenVPNConfigsServiceClient(conn)
	ctx := t.Context()
	id := &pb.OpenVPNInstanceRequest{Id: "primary"}
	assertStatus := func(want pb.OpenVPNInstanceState, hash string) {
		t.Helper()
		s, err := instances.GetStatus(ctx, id)
		if err != nil || s.GetState() != want || s.GetAppliedConfigSha256() != hash {
			t.Fatalf("GetStatus = %v, %v, want state %v hash %s", s, err, want, hash)
		}
	}
	assertStatus(pb.OpenVPNInstanceState_OPEN_VPN_INSTANCE_STATE_STOPPED, "")
	valid, err := configs.ValidateConfig(ctx, &pb.ValidateOpenVPNConfigRequest{Id: "primary"})
	if err != nil || !valid.GetValid() {
		t.Fatalf("ValidateConfig = %v, %v", valid, err)
	}
	invalid := f.request(t, http.MethodPost, "/config-validations", []byte("daemon\n"), http.StatusOK)
	if !bytes.Contains(invalid, []byte("unsupported_directive")) {
		t.Fatalf("unexpected validation result: %s", invalid)
	}
	explicitEmpty, err := configs.ValidateConfig(ctx, &pb.ValidateOpenVPNConfigRequest{Id: "primary", ConfigText: []byte{}})
	if err != nil || explicitEmpty.GetValid() || explicitEmpty.GetIssue().GetCode() != pb.OpenVPNConfigIssueCode_OPEN_VPN_CONFIG_ISSUE_CODE_INVALID_SOURCE {
		t.Fatalf("explicit empty source = %v, %v", explicitEmpty, err)
	}

	hash := sha256.Sum256(f.source)
	encoded := hex.EncodeToString(hash[:])
	started := f.request(t, http.MethodPut, "/execution", nil, http.StatusOK)
	if !bytes.Contains(started, []byte(encoded)) {
		t.Fatalf("unexpected Start status: %s", started)
	}
	assertStatus(pb.OpenVPNInstanceState_OPEN_VPN_INSTANCE_STATE_READY, encoded)
	if startedVersion, err := instances.GetStatus(ctx, id); err != nil || startedVersion.GetOpenvpnVersion() == "" {
		t.Fatalf("running version not confirmed: %v, %v", startedVersion, err)
	}
	f.tunnel(t)
	ops := pb.NewOpenVPNOperationsServiceClient(conn)
	report, err := ops.ListSessions(ctx, id)
	if err != nil || len(report.GetSessions()) != 1 || report.GetExecutionId() == "" {
		t.Fatalf("list sessions = %v, %v", report, err)
	}
	session := report.Sessions[0]
	if session.CommonName != "fixture-client" || session.TunnelIpv4 != "10.224.0.2" || session.RxBytes == 0 || session.TxBytes == 0 {
		t.Fatalf("unexpected session after tunneled traffic: %v", session)
	}
	httpReport := f.request(t, http.MethodGet, "/sessions", nil, http.StatusOK)
	if !bytes.Contains(httpReport, []byte(report.ExecutionId)) || !bytes.Contains(httpReport, []byte("fixture-client")) {
		t.Fatalf("HTTP sessions = %s", httpReport)
	}
	logCtx, stopLogs := context.WithTimeout(ctx, 3*time.Second)
	defer stopLogs()
	logs, err := ops.GetLogs(logCtx, &pb.OpenVPNLogsRequest{Id: id.Id, ExecutionId: report.ExecutionId})
	if err != nil {
		t.Fatal(err)
	}
	entriesBatch, err := logs.Recv()
	if err != nil || len(entriesBatch.GetEntries()) == 0 || entriesBatch.GetExecutionId() != report.ExecutionId {
		t.Fatalf("log batch = %v, %v", entriesBatch, err)
	}
	stopLogs()
	logRequest, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://"+f.httpAddr+"/v1/openvpn/instances/primary/logs?execution_id="+report.ExecutionId, nil)
	if err != nil {
		t.Fatal(err)
	}
	logReply, err := f.client.Do(logRequest)
	if err != nil {
		t.Fatal(err)
	}
	var httpLogBatch struct {
		ExecutionID string            `json:"execution_id"`
		Entries     []json.RawMessage `json:"entries"`
	}
	err = json.NewDecoder(logReply.Body).Decode(&httpLogBatch)
	logReply.Body.Close()
	if err != nil || logReply.StatusCode != http.StatusOK || httpLogBatch.ExecutionID != report.ExecutionId || len(httpLogBatch.Entries) == 0 {
		t.Fatalf("HTTP log batch = %+v, %v", httpLogBatch, err)
	}
	checked, err := configs.ValidateConfig(ctx, &pb.ValidateOpenVPNConfigRequest{Id: "primary", ConfigText: f.source})
	if err != nil || !checked.GetValid() {
		t.Fatalf("dry-run during active execution = %v, %v", checked, err)
	}

	replay, err := instances.Start(ctx, &pb.OpenVPNStartRequest{Id: "primary", ConfigText: f.source})
	if err != nil || replay.GetStartAction() != pb.OpenVPNStartAction_OPEN_VPN_START_ACTION_HEARTBEAT {
		t.Fatalf("replay = %v, %v", replay, err)
	}
	// The directive is permitted by Node, but its native argument is missing.
	_, err = instances.Start(ctx, &pb.OpenVPNStartRequest{Id: "primary", ConfigText: []byte("port\n")})
	if grpcstatus.Code(err) != codes.InvalidArgument {
		t.Fatalf("native parser did not reject the new config: %v", err)
	}
	details := grpcstatus.Convert(err).Details()
	if len(details) != 1 || details[0].(*pb.OpenVPNInstanceStatus).GetAppliedConfigSha256() != encoded {
		t.Fatalf("rejected Start did not report the previous config: %v", details)
	}
	assertStatus(pb.OpenVPNInstanceState_OPEN_VPN_INSTANCE_STATE_READY, encoded)
	f.ping(t)
	_, err = ops.DisconnectSession(ctx, &pb.DisconnectOpenVPNSessionRequest{Id: id.Id, ExecutionId: "stale", SessionId: &session.Id})
	if grpcstatus.Code(err) != codes.FailedPrecondition {
		t.Fatalf("stale disconnect = %v", err)
	}
	f.ping(t)
	f.request(t, http.MethodDelete, "/sessions/"+strconv.FormatUint(session.Id, 10)+"?execution_id="+report.ExecutionId, nil, http.StatusNoContent)
	until := time.Now().Add(2 * time.Second)
	for {
		updated, err := ops.ListSessions(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(updated.Sessions) == 0 || updated.Sessions[0].Id != session.Id {
			break
		}
		if time.Now().After(until) {
			t.Fatal("native client-kill did not remove the session")
		}
		time.Sleep(10 * time.Millisecond)
	}

	stopped, err := instances.Stop(ctx, id)
	if err != nil || stopped.GetState() != pb.OpenVPNInstanceState_OPEN_VPN_INSTANCE_STATE_STOPPED || stopped.AppliedConfigSha256 != nil {
		t.Fatalf("Stop = %v, %v", stopped, err)
	}
	entries, err := os.ReadDir(f.runtimeDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("Stop left runtime files: %v, %v", entries, err)
	}

	blocker, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(192, 0, 2, 1), Port: f.port})
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	_, err = instances.Start(ctx, &pb.OpenVPNStartRequest{Id: "primary"})
	if grpcstatus.Code(err) != codes.Unavailable {
		t.Fatalf("occupied UDP listener start = %v", err)
	}
	blocker.Close()
	assertStatus(pb.OpenVPNInstanceState_OPEN_VPN_INSTANCE_STATE_FAILED, "")
	entries, err = os.ReadDir(f.runtimeDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed Start left runtime files: %v, %v", entries, err)
	}

	proposed := append(append([]byte(nil), f.source...), '\n')
	body, err := json.Marshal(map[string]string{"config_text": string(proposed)})
	if err != nil {
		t.Fatal(err)
	}
	f.request(t, http.MethodPut, "/execution", body, http.StatusOK)
	hash = sha256.Sum256(proposed)
	assertStatus(pb.OpenVPNInstanceState_OPEN_VPN_INSTANCE_STATE_READY, hex.EncodeToString(hash[:]))
	f.request(t, http.MethodDelete, "/execution", nil, http.StatusOK)
}
