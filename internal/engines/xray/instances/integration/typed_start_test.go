package integration_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	accountsv1 "github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/native"
	instancesv1 "github.com/PapilioVPN/node/internal/engines/xray/instances/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
)

func TestTypedAccountsThroughGRPCStart(t *testing.T) {
	ports := availablePorts(t, 3)
	config := func(port int, rule string) []byte {
		return fmt.Appendf(nil, `{"log":{"loglevel":"none"},"inbounds":[
			{"tag":"legacy","listen":"127.0.0.1","port":%d,"protocol":"vless","settings":{"decryption":"none","clients":[]}},
			{"tag":"typed","listen":"127.0.0.1","port":%d,"protocol":"trojan","settings":{"clients":[]}}],
			"outbounds":[{"tag":"direct","protocol":"freedom"}],
			"routing":{"rules":[{"ruleTag":%q,"network":"tcp","outboundTag":"direct"}]}}`, port, ports[1], rule)
	}
	candidate := make(chan *grpc.ClientConn)
	replay := make(chan struct{})
	var service *lifecycle.Service
	f := newFixture(t, `{"log":{"loglevel":"none"}}`, fx.Invoke(func(instance *lifecycle.Service) {
		service = instance
		accounts := native.New(instance)
		instance.SetAccountInitializer(func(ctx context.Context, id string, conn *grpc.ClientConn,
			initial []lifecycle.InboundUsers, excluded []string) (func(), error) {
			if len(initial) == 0 {
				select {
				case candidate <- conn:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				select {
				case <-replay:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return accounts.Initialize(ctx, id, conn, initial, excluded)
		})
	}))
	typed := &instancesv1.XrayInitialAccount{Email: "initial",
		Credential: &accountsv1.Credential{Protocol: "trojan", Password: "initial-fixture"}}
	req := &instancesv1.XrayInstanceRequest{Id: "primary", ConfigJson: config(ports[0], "before"), KeepAliveSeconds: new(uint32(30)),
		InitialUsers: []*instancesv1.XrayInitialInboundUsers{
			{InboundTag: "legacy", Accounts: []*instancesv1.XrayInitialAccount{{Email: "legacy", Uuid: "d5120cd4-15b3-4ca7-a198-0a32d269c135"}}},
			{InboundTag: "typed", Accounts: []*instancesv1.XrayInitialAccount{typed}},
		}}
	first := f.startGRPC(req, instancesv1.XrayStartAction_XRAY_START_ACTION_STARTED)
	pid := f.childPID()
	f.expectHTTPCount("legacy", 1)
	conn, err := service.NativeConnection("primary")
	if err != nil {
		t.Fatal(err)
	}
	expectTrojan(t, f.ctx, conn, "initial", "initial-fixture")

	heartbeat := f.startGRPC(req, instancesv1.XrayStartAction_XRAY_START_ACTION_HEARTBEAT)
	if !heartbeat.GetKeepAliveExpiresAt().AsTime().After(first.GetKeepAliveExpiresAt().AsTime()) {
		t.Fatalf("heartbeat lease = %v, initial lease = %v", heartbeat.GetKeepAliveExpiresAt(), first.GetKeepAliveExpiresAt())
	}
	req.ConfigJson = config(ports[0], "live")
	f.startGRPC(req, instancesv1.XrayStartAction_XRAY_START_ACTION_LIVE_UPDATED)
	f.expectRule("live")
	if f.childPID() != pid {
		t.Fatal("heartbeat or live update replaced Xray")
	}
	expectTrojan(t, f.ctx, conn, "initial", "initial-fixture")

	// An incompatible config replaces Xray. Pause immediately before the real
	// account initializer so a premature Ready state cannot escape observation.
	req.ConfigJson = config(ports[2], "restarted")
	req.InitialUsers = nil
	restartCtx, cancel := context.WithCancel(f.ctx)
	done := make(chan struct{})
	var restarted *instancesv1.XrayInstanceStatus
	var restartErr error
	go func() {
		defer close(done)
		restarted, restartErr = f.client.Start(restartCtx, req)
	}()
	defer func() {
		cancel()
		<-done
	}()
	select {
	case conn := <-candidate:
		if len(trojanUsers(t, f.ctx, conn)) != 0 {
			t.Fatal("replacement Xray already contained accounts before replay")
		}
	case <-done:
		t.Fatalf("Start completed before replay = (%v, %v)", restarted, restartErr)
	case <-restartCtx.Done():
		t.Fatal(restartCtx.Err())
	}
	f.expectGRPC(f.client.GetStatus, instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_STARTING)
	if _, err := service.NativeConnection("primary"); !errors.Is(err, lifecycle.ErrNotReady) {
		t.Fatalf("candidate connection before replay = %v", err)
	}
	close(replay)
	<-done
	if restartErr != nil || restarted.GetState() != instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_READY ||
		restarted.GetStartAction() != instancesv1.XrayStartAction_XRAY_START_ACTION_RESTARTED {
		t.Fatalf("restart = (%v, %v)", restarted, restartErr)
	}
	if f.childPID() == pid {
		t.Fatal("restart retained the original Xray process")
	}
	f.expectRule("restarted")
	f.expectHTTPCount("legacy", 1)
	conn, err = service.NativeConnection("primary")
	if err != nil {
		t.Fatal(err)
	}
	expectTrojan(t, f.ctx, conn, "initial", "initial-fixture")
	f.expectGRPC(f.client.Stop, instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_STOPPED)
	f.expectClean()
}

func TestTypedInitialAccountFailureReapsCandidate(t *testing.T) {
	port := availablePorts(t, 1)[0]
	f := newFixture(t, `{"log":{"loglevel":"none"}}`)
	_, err := f.client.Start(f.ctx, &instancesv1.XrayInstanceRequest{Id: "primary",
		ConfigJson: fmt.Appendf(nil, `{"log":{"loglevel":"none"},"inbounds":[
			{"tag":"typed","listen":"127.0.0.1","port":%d,"protocol":"trojan","settings":{"clients":[]}}]}`, port),
		InitialUsers: []*instancesv1.XrayInitialInboundUsers{{InboundTag: "typed", Accounts: []*instancesv1.XrayInitialAccount{{
			Email: "wrong-protocol", Credential: &accountsv1.Credential{Protocol: "vless", Uuid: "d5120cd4-15b3-4ca7-a198-0a32d269c135"},
		}}}},
	})
	f.expectStartFailure(err, codes.Unavailable, instancesv1.XrayStartFailureReason_XRAY_START_FAILURE_REASON_INITIAL_ACCOUNTS_FAILED,
		instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_FAILED)
	f.expectClean()
}

func (f fixture) startGRPC(req *instancesv1.XrayInstanceRequest, action instancesv1.XrayStartAction) *instancesv1.XrayInstanceStatus {
	f.t.Helper()
	got, err := f.client.Start(f.ctx, req)
	if err != nil || got.GetState() != instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_READY || got.GetStartAction() != action {
		f.t.Fatalf("Start = (%v, %v), want ready and %s", got, err, action)
	}
	return got
}

func expectTrojan(t *testing.T, ctx context.Context, conn *grpc.ClientConn, email, password string) {
	t.Helper()
	users := trojanUsers(t, ctx, conn)
	if len(users) != 1 || users[0].GetEmail() != email {
		t.Fatalf("native Trojan membership does not contain exactly %q", email)
	}
	var account native.PasswordAccount
	if err := proto.Unmarshal(users[0].GetAccount().GetValue(), &account); err != nil || account.GetPassword() != password {
		t.Fatalf("native Trojan credential mismatch: %v", err)
	}
}

func trojanUsers(t *testing.T, ctx context.Context, conn *grpc.ClientConn) []*native.User {
	t.Helper()
	users := new(native.GetInboundUserResponse)
	if err := conn.Invoke(ctx, native.HandlerService+"GetInboundUsers", &native.GetInboundUserRequest{Tag: "typed"}, users); err != nil {
		t.Fatal(err)
	}
	return users.GetUsers()
}
