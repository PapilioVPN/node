//go:build integration && linux

package grpcapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/vless"
	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/bootstrap"
	xrayservice "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
	xrayprocess "github.com/PapilioVPN/node/internal/modules/core/instances/infra/xray/process"
	"github.com/PapilioVPN/node/internal/modules/xray/users/domain"
	userv1 "github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestLiveUserAdditionOverMTLSEnablesVLESSWithoutRemovingExistingUsers(t *testing.T) {
	for _, network := range []string{"unix", "tcp"} {
		t.Run(network, func(t *testing.T) {
			certificates := testutil.NewCertificates(t)
			first, healthA := testutil.ConfiguredXray(t, certificates.Directory, "edge-a")
			second, healthB := testutil.ConfiguredXray(t, certificates.Directory, "edge-b")
			selectedID, otherID, replacementID := "8e781ad6-dae6-4ed4-9d7b-8c247f735869", "4caa3ce4-39ab-49df-bc05-624befa32996", "c4fe8bca-4ce5-4d42-84c5-552b3923ced6"
			firstPorts := userTestVLESSInbounds(t, &first, selectedID, otherID)
			secondPorts := userTestVLESSInbounds(t, &second, selectedID, otherID)
			withoutSeededVLESSUser(t, first.Xray.ConfigFile)
			for _, instance := range []*settings.Instance{&first, &second} {
				testutil.ConfigureXrayAPI(t, instance, network, []string{"HandlerService"})
			}
			original, err := os.ReadFile(first.Xray.ConfigFile)
			if err != nil {
				t.Fatal(err)
			}
			clientConfig, proxies := userTestVLESSClient(t, certificates.Directory,
				[]int{firstPorts[0], firstPorts[0], firstPorts[1], secondPorts[0], firstPorts[0]},
				[]string{selectedID, otherID, selectedID, selectedID, replacementID})
			cfg := certificates.Config()
			first.Xray.Users = &settings.XrayUsers{MaxBatchUsers: 1}
			cfg.Instances = []settings.Instance{first, second}
			cfg.Communications.GRPC.Permissions.ManageEngineUsers = true
			cfg.Communications.GRPC.Permissions.ReadEngineUsers = true
			cfg.Communications.GRPC.Permissions.ReadEngineUserMetadata = true
			cfg.Communications.GRPC.Permissions.ReadEngineUserAccounts = true
			var server *rpc.Server
			var cleanup struct {
				fx.In
				Closers []io.Closer `group:"module_resources"`
			}
			application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&server, &cleanup))
			if err := application.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := application.Stop(context.Background()); err != nil {
					t.Error(err)
				}
				_ = server.Close()
				for _, closer := range cleanup.Closers {
					if err := closer.Close(); err != nil {
						t.Error(err)
					}
				}
				testutil.AssertTCPPortsReleased(t, healthA, healthB)
				for _, port := range append(firstPorts, secondPorts...) {
					testutil.AssertTCPPortsReleased(t, net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
				}
				if network == "tcp" {
					testutil.AssertTCPPortsReleased(t, first.Xray.API.Address, second.Xray.API.Address)
				}
			})
			proxy, err := xrayprocess.New(xrayprocess.Config{Binary: first.Xray.Binary, ConfigFile: clientConfig, StopTimeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = proxy.Close(); testutil.AssertTCPPortsReleased(t, proxies...) })
			if err := proxy.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if err := testutil.XrayTraffic(ctx, healthA, healthB, proxies[1], proxies[2], proxies[3]); err != nil {
				t.Fatal(err)
			}
			blocked, release := context.WithTimeout(ctx, 400*time.Millisecond)
			err = testutil.XrayTraffic(blocked, proxies[0])
			release()
			if err == nil {
				t.Fatal("unprovisioned user authenticated before addition")
			}
			client := xrayservice.NewXrayServiceClient(certificates.GRPCClient(t, server.Addr().String()))
			before, err := client.GetInboundUserCount(ctx, &userv1.GetInboundUserCountRequest{InstanceId: "edge-a", InboundTag: "target"})
			if err != nil || before.Count != 1 {
				t.Fatalf("native initial membership count: %v %v", before, err)
			}
			verifySingleUserViews(t, ctx, client, "edge-a", "target", "selected@local", nil)
			request := &userv1.AddInboundUserRequest{InstanceId: "edge-a", InboundTag: "target", User: &userv1.InboundUser{Email: "selected@local", Account: &userv1.InboundUser_Trojan{Trojan: &userv1.PasswordAccount{Password: "test-only"}}}}
			if _, err := client.AddInboundUser(ctx, request); status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("wrong account family reached native user manager: %v", err)
			}
			request.User.Account = &userv1.InboundUser_Vless{Vless: &userv1.VLESSAccount{Id: selectedID}}
			if _, err := client.AddInboundUsers(ctx, &userv1.AddInboundUsersRequest{InstanceId: "edge-b", InboundTag: "target"}); status.Code(err) != codes.FailedPrecondition {
				t.Fatal("omitted profile enabled collection writes")
			}

			if _, err := client.ApplyInboundUserDelta(ctx, &userv1.ApplyInboundUserDeltaRequest{InstanceId: "edge-b", InboundTag: "target"}); status.Code(err) != codes.FailedPrecondition {
				t.Fatal("omitted profile enabled delta writes")
			}
			if _, err := client.ApplyInboundUserDelta(ctx, &userv1.ApplyInboundUserDeltaRequest{InstanceId: "edge-a", InboundTag: "target", Users: []*userv1.InboundUser{request.User}, RemoveEmails: []string{"other@local"}}); status.Code(err) != codes.ResourceExhausted {
				t.Fatal("delta ignored combined replacement/removal limit")
			}
			batch := &userv1.AddInboundUsersRequest{InstanceId: "edge-a", InboundTag: "target", Users: []*userv1.InboundUser{request.User, request.User}}
			if _, err := client.AddInboundUsers(ctx, batch); status.Code(err) != codes.ResourceExhausted {
				t.Fatal("collection ignored its instance limit")
			}
			batch.Users = batch.Users[:1]
			if result, err := client.AddInboundUsers(ctx, batch); err != nil || len(result.Items) != 1 || result.Items[0].State != userv1.UserAdditionState_USER_ADDITION_STATE_ACKNOWLEDGED {
				t.Fatal("real native TCP/Unix collection failed")
			}
			if err := testutil.XrayTraffic(ctx, proxies[0]); err != nil {
				t.Fatal("collection-added user could not authenticate")
			}
			if _, err := client.RemoveInboundUser(ctx, &userv1.RemoveInboundUserRequest{InstanceId: "edge-a", InboundTag: "target", Email: request.User.Email}); err != nil {
				t.Fatal("collection traffic fixture cleanup failed")
			}

			started := time.Now().UnixMilli()
			ack, err := client.AddInboundUser(ctx, request)
			if err != nil || ack.InstanceId != "edge-a" || ack.InboundTag != "target" || ack.Email != "selected@local" || ack.AcknowledgedAtUnixMs < started {
				t.Fatalf("addition acknowledgement: %v %v", ack, err)
			}
			if err := testutil.XrayTraffic(ctx, proxies[0]); err != nil {
				t.Fatalf("added user cannot authenticate: %v", err)
			}
			for _, selection := range []struct{ instance, tag string }{{"edge-a", "target"}, {"edge-a", "other"}, {"edge-b", "target"}} {
				count, err := client.GetInboundUserCount(ctx, &userv1.GetInboundUserCountRequest{InstanceId: selection.instance, InboundTag: selection.tag})
				if err != nil || count.InstanceId != selection.instance || count.InboundTag != selection.tag || count.Count != 2 {
					t.Fatalf("count crossed instance/inbound membership: %v %v", count, err)
				}
			}

			// Read every selected inbound through the public views without changing live traffic.
			for _, selected := range []struct{ id, tag string }{{"edge-a", "target"}, {"edge-a", "other"}, {"edge-b", "target"}} {
				native := serial.ToTypedMessage(&vless.Account{Id: selectedID})
				record := domain.ObservedInboundUser{Email: "selected@local", Account: domain.ObservedUserAccount{TypeName: native.Type, Protobuf: native.Value}}
				verifySingleUserViews(t, ctx, client, selected.id, selected.tag, "selected@local", &record)
				all, err := client.ListInboundUserMetadata(ctx, &userv1.ListInboundUserMetadataRequest{InstanceId: selected.id, InboundTag: selected.tag})
				if err != nil || len(all.Users) != 2 {
					t.Fatal("public user list crossed instance/inbound membership")
				}
			}
			request.User.Account = &userv1.InboundUser_Vless{Vless: &userv1.VLESSAccount{Id: replacementID}}
			_, err = client.AddInboundUser(ctx, request)
			if status.Code(err) != codes.Unavailable {
				t.Fatalf("native duplicate was hidden or replaced: %v", err)
			}
			details := status.Convert(err).Details()
			if len(details) != 1 {
				t.Fatal("duplicate failure lost mutation outcome details")
			}
			failure, ok := details[0].(*userv1.UserMutationFailure)
			if !ok || failure.Outcome != userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_UNKNOWN {
				t.Fatal("generic native error was relabeled as proof of no effect")
			}
			blocked, release = context.WithTimeout(ctx, 400*time.Millisecond)
			err = testutil.XrayTraffic(blocked, proxies[4])
			release()
			if err == nil {
				t.Fatal("duplicate attempt silently replaced the original credential")
			}
			if err := testutil.XrayTraffic(ctx, proxies[0], proxies[1], proxies[2], proxies[3], healthA, healthB); err != nil {
				t.Fatalf("addition/duplicate removed existing users or crossed ownership: %v", err)
			}

			// A real replacement must disable the old credential and admit its successor
			// while the other user, inbound and instance keep accepting their sessions.
			progress, err := client.ApplyInboundUserDelta(ctx, &userv1.ApplyInboundUserDeltaRequest{InstanceId: "edge-a", InboundTag: "target", Users: []*userv1.InboundUser{request.User}})
			if err != nil || len(progress.Steps) != 2 || progress.Steps[0].State != userv1.UserDeltaState_USER_DELTA_STATE_ACKNOWLEDGED || progress.Steps[1].State != userv1.UserDeltaState_USER_DELTA_STATE_ACKNOWLEDGED {
				t.Fatal("public replacement did not acknowledge removal and addition")
			}

			blocked, release = context.WithTimeout(ctx, 400*time.Millisecond)
			err = testutil.XrayTraffic(blocked, proxies[0])
			release()
			if err == nil {
				t.Fatal("replaced VLESS credential still authenticated a new session")
			}
			if err := testutil.XrayTraffic(ctx, proxies[4], proxies[1], proxies[2], proxies[3], healthA, healthB); err != nil {
				t.Fatal("replacement failed authentication or crossed selected ownership")
			}
			current, err := os.ReadFile(first.Xray.ConfigFile)
			if err != nil || !bytes.Equal(current, original) {
				t.Fatalf("live addition modified persisted native config: %v", err)
			}
		})
	}
}

// Change only this isolated fixture before starting the engine. The other
// inbound and instance retain the selected credential to test scope isolation.
func withoutSeededVLESSUser(t *testing.T, path string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	for _, inbound := range config["inbounds"].([]any) {
		inbound := inbound.(map[string]any)
		if inbound["tag"] == "target" {
			settings := inbound["settings"].(map[string]any)
			settings["clients"] = settings["clients"].([]any)[1:]
		}
	}
	content, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}
