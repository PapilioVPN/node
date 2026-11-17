//go:build integration && linux

package grpcapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
	routingv1 "github.com/PapilioVPN/node/internal/apps/xray/network/routing/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/bootstrap"
	xrayservice "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestXrayRuleRemovalOverMTLSChangesTrafficAndKeepsOtherState(t *testing.T) {
	for _, network := range []string{"unix", "tcp"} {
		t.Run(network, func(t *testing.T) {
			certificates := testutil.NewCertificates(t)
			first, healthA := testutil.ConfiguredXray(t, certificates.Directory, "edge-a")
			second, healthB := testutil.ConfiguredXray(t, certificates.Directory, "edge-b")
			testutil.ConfigureXrayAPI(t, &first, network, []string{"RoutingService"})
			testutil.ConfigureXrayAPI(t, &second, network, []string{"RoutingService"})
			balancedA, configA := configureXrayRuleRemoval(t, first)
			balancedB, configB := configureXrayRuleRemoval(t, second)
			cfg := certificates.Config()
			cfg.Instances = []settings.Instance{first, second}
			cfg.Communications.GRPC.Permissions.ManageEngineRouting = true
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
				testutil.AssertTCPPortsReleased(t, healthA, healthB, balancedA, balancedB)
				if network == "tcp" {
					testutil.AssertTCPPortsReleased(t, first.Xray.API.Address, second.Xray.API.Address)
				}
			})
			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
			defer cancel()
			if err := testutil.XrayTraffic(ctx, healthA, healthB); err != nil {
				t.Fatal(err)
			}
			firstAPI, secondAPI := nativeXrayAPIClient(t, first), nativeXrayAPIClient(t, second)
			originalA := awaitNativeRoutingRules(t, ctx, firstAPI)
			originalB := awaitNativeRoutingRules(t, ctx, secondAPI)
			if len(originalA.Items) != 3 || originalA.Items[0].RuleTag != " balanced>>>rule " || originalA.Items[1].RuleTag != "   " {
				t.Fatalf("invalid native fixture rules: %+v", originalA)
			}
			if _, err := firstAPI.OverrideBalancerTarget(ctx, domain.BalancerOverride{BalancerTag: "pool", Target: "blocked"}); err != nil {
				t.Fatal(err)
			}
			assertBalancedHTTP(t, ctx, balancedA, false)
			assertBalancedHTTP(t, ctx, balancedB, true)
			client := xrayservice.NewXrayServiceClient(certificates.GRPCClient(t, server.Addr().String()))
			remove := func(tag string) {
				t.Helper()
				before := time.Now().UnixMilli()
				response, err := client.RemoveRoutingRule(ctx, &routingv1.RemoveRoutingRuleRequest{InstanceId: "edge-a", RuleTag: tag})
				if err != nil || response.InstanceId != "edge-a" || response.RuleTag != tag || response.AcknowledgedAtUnixMs < before || response.AcknowledgedAtUnixMs > time.Now().UnixMilli() {
					t.Fatalf("public removal acknowledgement changed: %v %v", response, err)
				}
			}
			remove(" balanced>>>rule ")
			assertBalancedHTTP(t, ctx, balancedA, true)
			actual, err := firstAPI.ReadRoutingRules(ctx)
			if err != nil || !reflect.DeepEqual(actual.Items, originalA.Items[1:]) {
				t.Fatalf("selected removal changed other rule order: %+v %v", actual, err)
			}
			probe := domain.RoutingProbe{Context: domain.RoutingContext{Network: domain.RoutingTCP, TargetDomain: "pool.invalid"}}
			if result, err := firstAPI.TestRoute(ctx, probe); err != nil || result.Route.OutboundTag != "blocked" {
				t.Fatalf("removal changed an unrelated rule/balancer: %+v %v", result, err)
			}
			remove("missing") // Native acknowledgement makes no prior-membership claim.
			remove(" balanced>>>rule ")
			if actual, err := firstAPI.ReadRoutingRules(ctx); err != nil || !reflect.DeepEqual(actual.Items, originalA.Items[1:]) {
				t.Fatalf("missing rule removal changed other rules: %+v %v", actual, err)
			}
			// Whitespace identifies a real native rule. Discard its successful response.
			response, lost := client.RemoveRoutingRule(ctx, &routingv1.RemoveRoutingRuleRequest{InstanceId: "edge-a", RuleTag: "   "}, grpc.MaxCallRecvMsgSize(1))
			if response != nil || status.Code(lost) != codes.ResourceExhausted {
				t.Fatalf("failed to discard removal acknowledgement: %v %v", response, lost)
			}
			if actual, err := firstAPI.ReadRoutingRules(ctx); err != nil || !reflect.DeepEqual(actual.Items, originalA.Items[2:]) {
				t.Fatalf("lost reply erased removal effect: %+v %v", actual, err)
			}
			if result, err := firstAPI.TestRoute(ctx, probe); err != nil || result.Route.OutboundTag != "edge-a-out-a" {
				t.Fatalf("removed rule still selected: %+v %v", result, err)
			}
			remove("   ")
			for _, tag := range []string{"pool", "other-pool"} {
				info, err := firstAPI.ReadBalancerInfo(ctx, tag)
				want := ""
				if tag == "pool" {
					want = "blocked"
				}
				if err != nil || info.OverrideTarget == nil || *info.OverrideTarget != want {
					t.Fatalf("rule removal replaced balancers/overrides: %+v %v", info, err)
				}
			}
			if actual, err := secondAPI.ReadRoutingRules(ctx); err != nil || !reflect.DeepEqual(actual.Items, originalB.Items) {
				t.Fatalf("rule removal crossed instance ownership: %+v %v", actual, err)
			}
			assertBalancedHTTP(t, ctx, balancedA, true)
			assertBalancedHTTP(t, ctx, balancedB, true)
			if err := testutil.XrayTraffic(ctx, healthA, healthB); err != nil {
				t.Fatal(err)
			}
			for _, item := range []struct {
				instance settings.Instance
				original []byte
			}{{first, configA}, {second, configB}} {
				content, err := os.ReadFile(item.instance.Xray.ConfigFile)
				if err != nil || !bytes.Equal(content, item.original) {
					t.Fatalf("live removal rewrote native config: %v", err)
				}
			}
		})
	}
}

func awaitNativeRoutingRules(t *testing.T, ctx context.Context, client *nativeXrayAPI) domain.RoutingRules {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := client.ReadRoutingRules(ctx)
		if err == nil {
			return result
		}
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("native routing API readiness: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
}

func configureXrayRuleRemoval(t *testing.T, instance settings.Instance) (string, []byte) {
	t.Helper()
	address, content := configureXrayBalancerOverride(t, instance)
	var config, routing map[string]json.RawMessage
	var rules []map[string]json.RawMessage
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(config["routing"], &routing); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(routing["rules"], &rules); err != nil {
		t.Fatal(err)
	}
	rules[0]["ruleTag"], rules[1]["ruleTag"] = json.RawMessage(`" balanced>>>rule "`), json.RawMessage(`"   "`)
	var err error
	if routing["rules"], err = json.Marshal(rules); err != nil {
		t.Fatal(err)
	}
	if config["routing"], err = json.Marshal(routing); err != nil {
		t.Fatal(err)
	}
	if content, err = json.Marshal(config); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(instance.Xray.ConfigFile, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return address, content
}
