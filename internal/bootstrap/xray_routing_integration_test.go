//go:build integration && linux

package bootstrap_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	routingnative "github.com/xtls/xray-core/app/router/command"
	netnative "github.com/xtls/xray-core/common/net"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/bootstrap"
	xrayv1 "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
	routingv1 "github.com/PapilioVPN/node/internal/modules/xray/routing/grpcapi/proto/v1"
	userv1 "github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

func TestXrayRoutingReadsOverMTLSKeepPriorityTrafficAndInstances(t *testing.T) {
	for _, network := range []string{"unix", "tcp"} {
		t.Run(network, func(t *testing.T) {
			certificates := testutil.NewCertificates(t)
			first, firstAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-a")
			second, secondAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-b")
			testutil.ConfigureXrayAPI(t, &first, network, []string{"RoutingService"})
			testutil.ConfigureXrayAPI(t, &second, network, []string{"RoutingService"})
			firstConfig := configureXrayRouting(t, first)
			secondConfig := configureXrayRouting(t, second)
			cfg := certificates.Config()
			cfg.Instances = []settings.Instance{first, second}
			cfg.Communications.GRPC.Permissions.ReadEngineRouting = true
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
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
				if err := server.Close(); err != nil {
					t.Error(err)
				}
				for _, closer := range cleanup.Closers {
					if err := closer.Close(); err != nil {
						t.Error(err)
					}
				}
				testutil.AssertTCPPortsReleased(t, firstAddress, secondAddress)
				if network == "tcp" {
					testutil.AssertTCPPortsReleased(t, first.Xray.API.Address, second.Xray.API.Address)
				}
			})
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if err := testutil.XrayTraffic(ctx, firstAddress, secondAddress); err != nil {
				t.Fatal(err)
			}
			client := xrayClient(t, server, certificates)
			firstRules := awaitRoutingRules(t, ctx, client, "edge-a")
			secondRules := awaitRoutingRules(t, ctx, client, "edge-b")
			assertRoutingReferences(t, firstRules, "edge-a")
			assertRoutingReferences(t, secondRules, "edge-b")

			// An independent test-only native probe verifies actual rule selection.
			// Node's two read operations never call TestRoute or publish a result.
			target := first.Xray.API.Address
			if network == "unix" {
				target = "unix://" + target
			}
			connection, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			native := routingnative.NewRoutingServiceClient(connection)
			assertPriority := func() {
				t.Helper()
				route, err := native.TestRoute(ctx, &routingnative.TestRouteRequest{RoutingContext: &routingnative.RoutingContext{
					Network: netnative.Network_TCP, TargetDomain: "priority.invalid", TargetPort: 443,
				}, PublishResult: false}, grpc.WaitForReady(true))
				if err != nil || route.GetOutboundTag() != "edge-a-out-a" {
					t.Fatalf("actual rule priority changed: %v %v", route, err)
				}
			}
			assertPriority()
			for i := 0; i < 3; i++ {
				rules, err := client.ListRoutingRules(ctx, &routingv1.ListRoutingRulesRequest{InstanceId: "edge-a"})
				if err != nil {
					t.Fatal(err)
				}
				assertRoutingReferences(t, rules, "edge-a")
				for _, id := range []string{"edge-a", "edge-b"} {
					info, err := client.GetBalancerInfo(ctx, &routingv1.GetBalancerInfoRequest{InstanceId: id, BalancerTag: "pool"})
					if err != nil || info.InstanceId != id || info.BalancerTag != "pool" || info.OverrideTarget == nil || *info.OverrideTarget != "" || info.PrincipalTargets == nil || info.ObservedAtUnixMs <= 0 {
						t.Fatalf("native balancer presence/identity: %v %v", info, err)
					}
					// Check instance membership independently of strategy-specific order.
					seen := make(map[string]int)
					for _, tag := range info.PrincipalTargets.Tags {
						seen[tag]++
					}
					if !reflect.DeepEqual(seen, map[string]int{id + "-out-a": 1, id + "-out-b": 1}) {
						t.Fatalf("candidate/instance isolation changed: %v", info)
					}
				}
			}
			assertPriority()
			for _, tag := range []string{"missing", " pool "} {
				// Pinned Xray returns a generic error for a missing balancer. Never
				// parse its text into NotFound or fabricate an empty observation.
				response, err := client.GetBalancerInfo(ctx, &routingv1.GetBalancerInfoRequest{InstanceId: "edge-a", BalancerTag: tag})
				if response != nil || status.Code(err) != codes.Unavailable || strings.Contains(err.Error(), certificates.Directory) {
					t.Fatalf("native missing tag was normalized or fabricated: %v %v", response, err)
				}
			}
			if err := testutil.XrayTraffic(ctx, firstAddress, secondAddress); err != nil {
				t.Fatal(err)
			}
			for _, item := range []struct {
				instance settings.Instance
				original []byte
			}{{first, firstConfig}, {second, secondConfig}} {
				current, err := os.ReadFile(item.instance.Xray.ConfigFile)
				if err != nil || !bytes.Equal(current, item.original) {
					t.Fatalf("routing read modified native config: %v", err)
				}
			}
		})
	}
}

func configureXrayRouting(t *testing.T, instance settings.Instance) []byte {
	t.Helper()
	content, err := os.ReadFile(instance.Xray.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	id := string(instance.ID)
	config["outbounds"] = json.RawMessage(fmt.Sprintf(`[{"tag":%q,"protocol":"freedom"},{"tag":%q,"protocol":"freedom"}]`, id+"-out-a", id+"-out-b"))
	config["routing"] = json.RawMessage(fmt.Sprintf(`{
	 "rules":[
	  {"type":"field","domain":["full:priority.invalid"],"outboundTag":%q,"ruleTag":"z-first"},
	  {"type":"field","domain":["full:priority.invalid"],"outboundTag":%q,"ruleTag":"a-second"},
	  {"type":"field","domain":["full:pool.invalid"],"balancerTag":"pool","ruleTag":"balanced"},
	  {"type":"field","network":"tcp,udp","outboundTag":%q}
	 ],
	 "balancers":[{"tag":"pool","selector":[%q],"strategy":{"type":"random"}}]
	}`, id+"-out-a", id+"-out-b", id+"-out-b", id+"-out-"))
	content, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(instance.Xray.ConfigFile, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return content
}

func assertRoutingReferences(t *testing.T, result *routingv1.ListRoutingRulesResponse, id string) {
	t.Helper()
	want := [][2]string{{id + "-out-a", "z-first"}, {id + "-out-b", "a-second"}, {"", "balanced"}, {id + "-out-b", ""}}
	if result.InstanceId != id || len(result.Rules) != len(want) || result.ObservedAtUnixMs <= 0 {
		t.Fatalf("native rule observation: %v", result)
	}
	for i, rule := range result.Rules {
		if rule.OutboundTag != want[i][0] || rule.RuleTag != want[i][1] {
			t.Fatalf("rule priority/reference changed: %v", result)
		}
	}
}

func awaitRoutingRules(t *testing.T, ctx context.Context, client xrayv1.XrayServiceClient, id string) *routingv1.ListRoutingRulesResponse {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		response, err := client.ListRoutingRules(ctx, &routingv1.ListRoutingRulesRequest{InstanceId: id})
		if err == nil {
			return response
		}
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("unexpected routing readiness error: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("native routing API did not become ready: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestRoutingReadGrantOverMTLSIsIndependent(t *testing.T) {
	for _, granted := range []bool{false, true} {
		t.Run(fmt.Sprint(granted), func(t *testing.T) {
			certificates := testutil.NewCertificates(t)
			cfg := certificates.Config()
			p := &cfg.Communications.GRPC.Permissions
			p.ReadEngineRouting = granted
			p.ReadEngineInfo, p.ReadEngineStats, p.ReadEngineOnline, p.ReadEngineUsers = !granted, !granted, !granted, !granted
			p.ReadInstanceStatus, p.ControlInstances, p.ValidateInstanceConfig, p.ResetEngineStats, p.ManageEngineUsers = !granted, !granted, !granted, !granted, !granted
			var server *rpc.Server
			application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&server))
			if err := application.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = application.Stop(context.Background()); _ = server.Close() })
			client := xrayClient(t, server, certificates)
			want := codes.PermissionDenied
			if granted {
				want = codes.NotFound
			}
			if result, err := client.ListRoutingRules(t.Context(), &routingv1.ListRoutingRulesRequest{InstanceId: "missing"}); result != nil || status.Code(err) != want {
				t.Fatalf("routing grant/lookup boundary changed: %v %v", result, err)
			}
			if result, err := client.GetBalancerInfo(t.Context(), &routingv1.GetBalancerInfoRequest{InstanceId: "missing", BalancerTag: "pool"}); result != nil || status.Code(err) != want {
				t.Fatalf("balancer grant/lookup boundary changed: %v %v", result, err)
			}
			if !granted {
				if _, err := client.ListRoutingRules(t.Context(), &routingv1.ListRoutingRulesRequest{}); status.Code(err) != codes.PermissionDenied {
					t.Fatalf("list shape checked before grant: %v", err)
				}
				if _, err := client.GetBalancerInfo(t.Context(), &routingv1.GetBalancerInfoRequest{}); status.Code(err) != codes.PermissionDenied {
					t.Fatalf("balancer shape checked before grant: %v", err)
				}
			} else {
				if _, err := client.GetInboundUserCount(t.Context(), &userv1.GetInboundUserCountRequest{}); status.Code(err) != codes.PermissionDenied {
					t.Fatalf("routing enabled user observations: %v", err)
				}
				if _, err := client.RemoveInboundUser(t.Context(), &userv1.RemoveInboundUserRequest{}); status.Code(err) != codes.PermissionDenied {
					t.Fatalf("routing enabled mutations: %v", err)
				}
			}
		})
	}
}
