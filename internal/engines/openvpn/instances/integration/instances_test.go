package integration_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"testing"

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
