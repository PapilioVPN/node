//go:build integration && linux

package bootstrap_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/bootstrap"
	xrayprocess "github.com/PapilioVPN/node/internal/modules/core/instances/infra/xray/process"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/xray/info/grpcapi/proto/v1"
	userv1 "github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

func TestLiveUserRemovalOverMTLSDeniesNewVLESSSessionsWithoutCrossingOwnership(t *testing.T) {
	for _, network := range []string{"unix", "tcp"} {
		t.Run(network, func(t *testing.T) {
			certificates := testutil.NewCertificates(t)
			first, healthA := configuredXray(t, certificates.Directory, "edge-a")
			second, healthB := configuredXray(t, certificates.Directory, "edge-b")
			selectedID, otherID := "8e781ad6-dae6-4ed4-9d7b-8c247f735869", "4caa3ce4-39ab-49df-bc05-624befa32996"
			firstPorts := userTestVLESSInbounds(t, &first, selectedID, otherID)
			secondPorts := userTestVLESSInbounds(t, &second, selectedID, otherID)
			for _, instance := range []*settings.Instance{&first, &second} {
				configureXrayAPI(t, instance, network, []string{"HandlerService"})
			}
			original, err := os.ReadFile(first.Xray.ConfigFile)
			if err != nil {
				t.Fatal(err)
			}
			clientConfig, proxies := userTestVLESSClient(t, certificates.Directory, []int{firstPorts[0], firstPorts[0], firstPorts[1], secondPorts[0]}, []string{selectedID, otherID, selectedID, selectedID})
			cfg := certificates.Config()
			cfg.Instances = []settings.Instance{first, second}
			cfg.Communications.GRPC.Permissions.ManageEngineUsers = true
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
				assertInstancePortsReleased(t, healthA, healthB)
				for _, port := range append(firstPorts, secondPorts...) {
					assertInstancePortsReleased(t, net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
				}
				if network == "tcp" {
					assertInstancePortsReleased(t, first.Xray.API.Address, second.Xray.API.Address)
				}
			})
			proxy, err := xrayprocess.New(xrayprocess.Config{Binary: first.Xray.Binary, ConfigFile: clientConfig, StopTimeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := proxy.Close(); err != nil {
					t.Error(err)
				}
				assertInstancePortsReleased(t, proxies...)
			})
			if err := proxy.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if err := xrayTraffic(ctx, healthA, healthB); err != nil {
				t.Fatalf("server readiness: %v", err)
			}
			if err := xrayTraffic(ctx, proxies...); err != nil {
				state, failure := proxy.Observe()
				t.Fatalf("all isolated credentials must work before removal: %v; client=%v/%v", err, state, failure)
			}
			client := xrayClient(t, server, certificates)
			started := time.Now().UnixMilli()
			ack, err := client.RemoveInboundUser(ctx, &userv1.RemoveInboundUserRequest{InstanceId: "edge-a", InboundTag: "target", Email: "selected@local"})
			if err != nil || ack.InstanceId != "edge-a" || ack.InboundTag != "target" || ack.Email != "selected@local" || ack.AcknowledgedAtUnixMs < started {
				t.Fatalf("native removal acknowledgement: %v %v", ack, err)
			}
			blocked, release := context.WithTimeout(ctx, 400*time.Millisecond)
			err = xrayTraffic(blocked, proxies[0])
			release()
			if err == nil {
				t.Fatal("removed credential still authenticated a new VLESS connection")
			}
			if err := xrayTraffic(ctx, proxies[1], proxies[2], proxies[3], healthA, healthB); err != nil {
				t.Fatalf("removal crossed user/inbound/instance ownership or stopped an engine: %v", err)
			}
			if _, err := client.GetRuntimeInfo(ctx, &xrayv1.GetRuntimeInfoRequest{InstanceId: "edge-a"}); status.Code(err) != codes.PermissionDenied {
				t.Fatalf("user management expanded observation grants: %v", err)
			}
			current, err := os.ReadFile(first.Xray.ConfigFile)
			if err != nil || !bytes.Equal(current, original) {
				t.Fatalf("live removal modified native config: %v", err)
			}
			_, err = client.RemoveInboundUser(ctx, &userv1.RemoveInboundUserRequest{InstanceId: "edge-a", InboundTag: "target", Email: "selected@local"})
			if status.Code(err) != codes.Unavailable {
				t.Fatalf("generic native absence was relabeled: %v", err)
			}
			details := status.Convert(err).Details()
			if len(details) != 1 {
				t.Fatalf("unknown outcome detail absent: %v", err)
			}
			failure, ok := details[0].(*userv1.UserMutationFailure)
			if !ok || failure.Outcome != userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_UNKNOWN {
				t.Fatalf("native generic failure became known no effect: %v", details)
			}
		})
	}
}

func userTestPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func TestUserManagementRequiresIndependentGrantOverMTLS(t *testing.T) {
	for _, granted := range []bool{false, true} {
		t.Run(fmt.Sprint(granted), func(t *testing.T) {
			certificates := testutil.NewCertificates(t)
			cfg := certificates.Config()
			p := &cfg.Communications.GRPC.Permissions
			p.ManageEngineUsers = granted
			p.ControlInstances = !granted
			p.ValidateInstanceConfig = !granted
			p.ReadInstanceStatus = !granted
			p.ReadEngineInfo = !granted
			p.ReadEngineStats = !granted
			p.ReadEngineOnline = !granted
			p.ResetEngineStats = !granted
			var server *rpc.Server
			application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&server))
			if err := application.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = application.Stop(context.Background()); _ = server.Close() })
			client := xrayClient(t, server, certificates)
			_, additionError := client.AddInboundUser(t.Context(), &userv1.AddInboundUserRequest{InstanceId: "missing", InboundTag: "in", User: &userv1.InboundUser{Email: "user", Account: &userv1.InboundUser_Vless{Vless: &userv1.VLESSAccount{Id: "test-only"}}}})
			additionCode := codes.PermissionDenied
			if granted {
				additionCode = codes.NotFound
			}
			if status.Code(additionError) != additionCode {
				t.Fatalf("addition did not enforce/wire the independent grant: %v", additionError)
			}
			_, err := client.RemoveInboundUser(t.Context(), &userv1.RemoveInboundUserRequest{InstanceId: "missing", InboundTag: "in", Email: "user"})
			if !granted {
				if status.Code(err) != codes.PermissionDenied {
					t.Fatalf("other grants enabled user removal: %v", err)
				}
				return
			}
			if status.Code(err) != codes.NotFound {
				t.Fatalf("user management not wired: %v", err)
			}
			details := status.Convert(err).Details()
			if len(details) != 1 {
				t.Fatalf("no-effect detail missing: %v", err)
			}
			failure, ok := details[0].(*userv1.UserMutationFailure)
			if !ok || failure.Outcome != userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_NOT_APPLIED {
				t.Fatalf("missing instance effects: %v", details)
			}
			if _, err := client.GetRuntimeInfo(t.Context(), &xrayv1.GetRuntimeInfoRequest{InstanceId: "missing"}); status.Code(err) != codes.PermissionDenied {
				t.Fatalf("user management expanded read grants: %v", err)
			}
		})
	}
}

func userTestVLESSInbounds(t *testing.T, instance *settings.Instance, selectedID, otherID string) []int {
	t.Helper()
	content, err := os.ReadFile(instance.Xray.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	var configuration map[string]json.RawMessage
	if err := json.Unmarshal(content, &configuration); err != nil {
		t.Fatal(err)
	}
	var inbounds []json.RawMessage
	if err := json.Unmarshal(configuration["inbounds"], &inbounds); err != nil {
		t.Fatal(err)
	}
	ports := []int{userTestPort(t), userTestPort(t)}
	// Native VLESS defaults block private destinations. Only this isolated fixture
	// permits its loopback HTTP test servers; Node never changes engine policies.
	configuration["outbounds"] = json.RawMessage(`[{"protocol":"freedom","settings":{"finalRules":[{"action":"allow","ip":["127.0.0.1/32"]}]}}]`)
	for i, tag := range []string{"target", "other"} {
		inbound, err := json.Marshal(map[string]any{"tag": tag, "listen": "127.0.0.1", "port": ports[i], "protocol": "vless", "settings": map[string]any{"decryption": "none", "clients": []map[string]string{{"id": selectedID, "email": "selected@local"}, {"id": otherID, "email": "other@local"}}}})
		if err != nil {
			t.Fatal(err)
		}
		inbounds = append(inbounds, inbound)
	}
	configuration["inbounds"], err = json.Marshal(inbounds)
	if err != nil {
		t.Fatal(err)
	}
	content, err = json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(instance.Xray.ConfigFile, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return ports
}

func userTestVLESSClient(t *testing.T, directory string, serverPorts []int, ids []string) (string, []string) {
	t.Helper()
	var inbounds, outbounds, rules []map[string]any
	addresses := make([]string, len(serverPorts))
	for i, port := range serverPorts {
		proxyPort := userTestPort(t)
		addresses[i] = net.JoinHostPort("127.0.0.1", fmt.Sprint(proxyPort))
		in, out := fmt.Sprint("in-", i), fmt.Sprint("out-", i)
		inbounds = append(inbounds, map[string]any{"tag": in, "listen": "127.0.0.1", "port": proxyPort, "protocol": "http", "settings": map[string]any{}})
		outbounds = append(outbounds, map[string]any{"tag": out, "protocol": "vless", "settings": map[string]any{"vnext": []map[string]any{{"address": "127.0.0.1", "port": port, "users": []map[string]string{{"id": ids[i], "encryption": "none"}}}}}})
		rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{in}, "outboundTag": out})
	}
	content, err := json.Marshal(map[string]any{"log": map[string]string{"loglevel": "none"}, "inbounds": inbounds, "outbounds": outbounds, "routing": map[string]any{"rules": rules}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "vless-client.json")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, addresses
}
