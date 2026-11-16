//go:build integration && linux

package bootstrap_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/geodata"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/PapilioVPN/node/internal/bootstrap"
	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/app/services"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
	xrayprocess "github.com/PapilioVPN/node/internal/modules/engines/xray/infra/process"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

func TestXrayRuleAdditionNativePipelinePreservesAppendReplaceAssetsAndInstances(t *testing.T) {
	for _, network := range []string{"unix", "tcp"} {
		t.Run(network, func(t *testing.T) {
			certificates := testutil.NewCertificates(t)
			first, healthA := configuredXray(t, certificates.Directory, "edge-a")
			second, healthB := configuredXray(t, certificates.Directory, "edge-b")
			configureXrayAPI(t, &first, network, []string{"RoutingService"})
			configureXrayAPI(t, &second, network, []string{"RoutingService"})
			balancedA, configA := configureXrayBalancerOverride(t, first)
			balancedB, configB := configureXrayBalancerOverride(t, second)
			copyRoutingBinaryAndSite(t, &first, "first.invalid")
			copyRoutingBinaryAndSite(t, &second, "second.invalid")
			workA, workB := t.TempDir(), t.TempDir()
			first.Xray.RoutingRules = &settings.RoutingRulePreparation{WorkDir: workA, MaxInputBytes: 256, MaxOutputBytes: 65536, MaxConcurrent: 1}
			second.Xray.RoutingRules = &settings.RoutingRulePreparation{WorkDir: workB, MaxInputBytes: 65536, MaxOutputBytes: 65536, MaxConcurrent: 1}
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
				assertInstancePortsReleased(t, healthA, healthB, balancedA, balancedB)
				if network == "tcp" {
					assertInstancePortsReleased(t, first.Xray.API.Address, second.Xray.API.Address)
				}
			})
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			if err := xrayTraffic(ctx, healthA, healthB); err != nil {
				t.Fatal(err)
			}
			firstAPI, secondAPI := nativeXrayAPIClient(t, first), nativeXrayAPIClient(t, second)
			originalA, originalB := awaitNativeRoutingRules(t, ctx, firstAPI), awaitNativeRoutingRules(t, ctx, secondAPI)
			if _, err := firstAPI.OverrideBalancerTarget(ctx, domain.BalancerOverride{BalancerTag: "pool", Target: "blocked"}); err != nil {
				t.Fatal(err)
			}
			bindings := map[instances.InstanceID]services.RoutingRuleBinding{}
			for _, item := range []struct {
				instance settings.Instance
				work     string
				applier  services.RoutingRuleApplier
			}{{first, workA, firstAPI}, {second, workB, secondAPI}} {
				preparer, err := xrayprocess.NewRoutingRulePreparer(xrayprocess.RoutingRulePreparationConfig{Binary: item.instance.Xray.Binary, Directory: filepath.Dir(item.instance.Xray.ConfigFile), WorkDir: item.work, Timeout: 2 * time.Second, MaxInputBytes: 65536, MaxOutputBytes: 65536, MaxConcurrent: 1})
				if err != nil {
					t.Fatal(err)
				}
				bindings[item.instance.ID] = services.RoutingRuleBinding{Preparer: preparer, Applier: item.applier, Timeout: 2 * time.Second}
			}
			service := services.NewRoutingRuleAdditionService(bindings)
			add := func(id instances.InstanceID, content string, mode domain.RoutingRuleMode, tag string) {
				t.Helper()
				before := time.Now()
				result, err := service.AddRoutingRule(ctx, id, domain.RoutingRuleAddition{JSON: []byte(content), Mode: mode})
				if err != nil || result.InstanceID != id || result.RuleTag != tag || result.Mode != mode || result.Acknowledgement.AcknowledgedAt.Before(before) || result.Acknowledgement.AcknowledgedAt.After(time.Now()) {
					t.Fatalf("native prepare/apply acknowledgement: %+v %v", result, err)
				}
			}
			// Appended rules come after the existing catch-all, retaining its precedence
			// and the existing balancer override. A prepend emulation would change traffic.
			content := `{"ruleTag":" appended ","inboundTag":["balanced-http"],"outboundTag":"edge-a-out-a"}`
			add("edge-a", content, domain.AppendRoutingRule, " appended ")
			afterAppend, err := firstAPI.ReadRoutingRules(ctx)
			expected := append(append([]domain.RoutingRuleReference{}, originalA.Items...), domain.RoutingRuleReference{RuleTag: " appended ", OutboundTag: "edge-a-out-a"})
			if err != nil || !reflect.DeepEqual(afterAppend.Items, expected) {
				t.Fatalf("append reordered/removed existing rules: %+v %v", afterAppend, err)
			}
			assertBalancedHTTP(t, ctx, balancedA, false) // Existing balanced route still wins.
			if result, err := service.AddRoutingRule(ctx, "edge-a", domain.RoutingRuleAddition{JSON: []byte(content), Mode: domain.AppendRoutingRule}); !errors.Is(err, domain.ErrRoutingMutationUnknown) || result != (domain.InstanceRoutingRuleAddition{}) {
				t.Fatalf("native duplicate became success/known rejection: %+v %v", result, err)
			}
			if actual, err := firstAPI.ReadRoutingRules(ctx); err != nil || !reflect.DeepEqual(actual.Items, expected) {
				t.Fatal("duplicate error altered live rule order")
			}

			if _, err := service.AddRoutingRule(ctx, "edge-a", domain.RoutingRuleAddition{JSON: []byte(`{}`), Mode: domain.AppendRoutingRule}); !errors.Is(err, domain.ErrRoutingRulePreparationFailed) || !errors.Is(err, domain.ErrInvalidRoutingRule) || errors.Is(err, domain.ErrRoutingMutationUnknown) {
				t.Fatalf("preparation failure lost known stage: %v", err)
			}
			// Native building preserves regex text; engine condition construction
			// can still reject it. That error follows dispatch, not preparation.
			invalidCondition := `{"ruleTag":"bad-regex","domain":["regexp:("],"outboundTag":"blocked"}`
			if _, err := service.AddRoutingRule(ctx, "edge-a", domain.RoutingRuleAddition{JSON: []byte(invalidCondition), Mode: domain.AppendRoutingRule}); !errors.Is(err, domain.ErrRoutingMutationUnknown) || errors.Is(err, domain.ErrRoutingRulePreparationFailed) {
				t.Fatalf("native runtime rejection was narrowed to preparation failure: %v", err)
			}
			if actual, err := firstAPI.ReadRoutingRules(ctx); err != nil || !reflect.DeepEqual(actual.Items, expected) {
				t.Fatal("failed preparation/native condition changed rule order")
			}
			balancerRule := `{"ruleTag":"balancer-rule","domain":["full:appended.invalid"],"balancerTag":"pool"}`
			add("edge-a", balancerRule, domain.AppendRoutingRule, "balancer-rule")
			if _, err := service.AddRoutingRule(ctx, "edge-a", domain.RoutingRuleAddition{JSON: []byte(balancerRule), Mode: domain.ReplaceRoutingRules}); !errors.Is(err, domain.ErrRoutingMutationUnknown) {
				t.Fatalf("replacement silently retained an undeclared balancer: %v", err)
			}
			if info, err := firstAPI.ReadBalancerInfo(ctx, "pool"); err != nil || info.OverrideTarget == nil || *info.OverrideTarget != "blocked" {
				t.Fatalf("failed native replacement changed old balancer: %+v %v", info, err)
			}
			// Replacement drops the old balanced route and balancers, rather than inserting
			// before them. This direct outbound permits actual HTTP despite the old override.
			add("edge-a", `{"ruleTag":"replacement","network":"tcp,udp","outboundTag":"edge-a-out-a"}`, domain.ReplaceRoutingRules, "replacement")
			assertBalancedHTTP(t, ctx, balancedA, true)
			if rules, err := firstAPI.ReadRoutingRules(ctx); err != nil || len(rules.Items) != 1 || rules.Items[0].RuleTag != "replacement" {
				t.Fatalf("replacement retained old rules: %+v %v", rules, err)
			}
			if _, err := firstAPI.ReadBalancerInfo(ctx, "pool"); err == nil {
				t.Fatal("replacement retained an old balancer/override")
			}
			if rules, err := secondAPI.ReadRoutingRules(ctx); err != nil || !reflect.DeepEqual(rules.Items, originalB.Items) {
				t.Fatalf("addition crossed instance boundary: %+v %v", rules, err)
			}
			assertBalancedHTTP(t, ctx, balancedB, true)
			// The same geosite code names different domains beside each selected binary.
			// Preparation and actual engine matching must both use that instance's files.
			geoRule := `{"ruleTag":"geo","domain":["geosite:TEST"],"outboundTag":"blocked"}`
			for _, id := range []instances.InstanceID{"edge-a", "edge-b"} {
				add(id, geoRule, domain.ReplaceRoutingRules, "geo")
			}
			for _, item := range []struct {
				id         instances.InstanceID
				own, other string
			}{{"edge-a", "first.invalid", "second.invalid"}, {"edge-b", "second.invalid", "first.invalid"}} {
				native := firstAPI
				if item.id == "edge-b" {
					native = secondAPI
				}
				if result, err := native.TestRoute(ctx, domain.RoutingProbe{Context: domain.RoutingContext{Network: domain.RoutingTCP, TargetDomain: item.own}}); err != nil || result.Route.OutboundTag != "blocked" {
					t.Fatalf("selected instance geosite rule not applied: %+v %v", result, err)
				}
				if result, err := native.TestRoute(ctx, domain.RoutingProbe{Context: domain.RoutingContext{Network: domain.RoutingTCP, TargetDomain: item.other}}); err == nil || !errors.Is(err, domain.ErrRoutingProbeUnknown) || !reflect.DeepEqual(result, domain.RoutingProbeResult{}) {
					t.Fatalf("geosite state borrowed across instances: %+v %v", result, err)
				}
			}

			// The production module owns these native clients/preparers. Only the
			// routing management grant is enabled on the external mTLS boundary.
			public := xrayClient(t, server, certificates)
			publicAdd := func(content string, mode xrayv1.RoutingRuleMode, tag string) {
				t.Helper()
				before := time.Now().UnixMilli()
				response, err := public.AddRoutingRule(ctx, &xrayv1.AddRoutingRuleRequest{InstanceId: "edge-a", RuleJson: []byte(content), Mode: mode})
				if err != nil || response.InstanceId != "edge-a" || response.RuleTag != tag || response.Mode != mode || response.AcknowledgedAtUnixMs < before || response.AcknowledgedAtUnixMs > time.Now().UnixMilli() {
					t.Fatalf("mTLS addition acknowledgement changed: %v %v", response, err)
				}
			}
			publicAdd(`{"network":"tcp","outboundTag":"edge-a-out-a"}`, xrayv1.RoutingRuleMode_ROUTING_RULE_MODE_APPEND, "")
			publicAdd(`{"ruleTag":"   ","network":"tcp","outboundTag":"blocked"}`, xrayv1.RoutingRuleMode_ROUTING_RULE_MODE_APPEND, "   ")
			rules, err := firstAPI.ReadRoutingRules(ctx)
			if err != nil || len(rules.Items) != 3 || rules.Items[0].RuleTag != "geo" || rules.Items[1].RuleTag != "" || rules.Items[2].RuleTag != "   " {
				t.Fatalf("mTLS append lost native tag/order semantics: %+v %v", rules, err)
			}
			_, err = public.AddRoutingRule(ctx, &xrayv1.AddRoutingRuleRequest{InstanceId: "edge-a", RuleJson: []byte(strings.Repeat(" ", 257)), Mode: xrayv1.RoutingRuleMode_ROUTING_RULE_MODE_APPEND})
			assertRoutingMutationFailure(t, err, codes.ResourceExhausted, xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
			_, err = public.AddRoutingRule(ctx, &xrayv1.AddRoutingRuleRequest{InstanceId: "edge-a", RuleJson: []byte(`{}`), Mode: xrayv1.RoutingRuleMode_ROUTING_RULE_MODE_APPEND})
			assertRoutingMutationFailure(t, err, codes.InvalidArgument, xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
			_, err = public.AddRoutingRule(ctx, &xrayv1.AddRoutingRuleRequest{InstanceId: "edge-a", RuleJson: []byte(invalidCondition), Mode: xrayv1.RoutingRuleMode_ROUTING_RULE_MODE_APPEND})
			assertRoutingMutationFailure(t, err, codes.Unavailable, xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_UNKNOWN)
			if actual, err := firstAPI.ReadRoutingRules(ctx); err != nil || !reflect.DeepEqual(actual.Items, rules.Items) {
				t.Fatal("failed public input/native condition changed live rules")
			}
			if err := os.Remove(workA); err != nil {
				t.Fatal(err)
			}
			_, err = public.AddRoutingRule(ctx, &xrayv1.AddRoutingRuleRequest{InstanceId: "edge-a", RuleJson: []byte(`{"network":"tcp","outboundTag":"blocked"}`), Mode: xrayv1.RoutingRuleMode_ROUTING_RULE_MODE_APPEND})
			assertRoutingMutationFailure(t, err, codes.Unavailable, xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
			if strings.Contains(err.Error(), workA) {
				t.Fatal("preparation failure exposed private paths")
			}
			if err := os.Mkdir(workA, 0o700); err != nil {
				t.Fatal(err)
			}
			publicAdd(`{"ruleTag":"public-block","inboundTag":["balanced-http"],"outboundTag":"blocked"}`, xrayv1.RoutingRuleMode_ROUTING_RULE_MODE_REPLACE, "public-block")
			assertBalancedHTTP(t, ctx, balancedA, false)
			assertBalancedHTTP(t, ctx, balancedB, true)
			lost, lostError := public.AddRoutingRule(ctx, &xrayv1.AddRoutingRuleRequest{InstanceId: "edge-a", RuleJson: []byte(`{"ruleTag":"lost-reply","network":"tcp,udp","outboundTag":"edge-a-out-a"}`), Mode: xrayv1.RoutingRuleMode_ROUTING_RULE_MODE_REPLACE}, grpc.MaxCallRecvMsgSize(1))
			if lost != nil || status.Code(lostError) != codes.ResourceExhausted {
				t.Fatalf("test did not discard the public acknowledgement: %v %v", lost, lostError)
			}
			if actual, err := firstAPI.ReadRoutingRules(ctx); err != nil || len(actual.Items) != 1 || actual.Items[0].RuleTag != "lost-reply" {
				t.Fatalf("lost acknowledgement erased actual replacement: %+v %v", actual, err)
			}
			assertBalancedHTTP(t, ctx, balancedA, true)
			if actual, err := secondAPI.ReadRoutingRules(ctx); err != nil || len(actual.Items) != 1 || actual.Items[0].RuleTag != "geo" {
				t.Fatalf("public addition crossed instance: %+v %v", actual, err)
			}
			if _, err := public.ListRoutingRules(ctx, &xrayv1.ListRoutingRulesRequest{InstanceId: "edge-a"}); status.Code(err) != codes.PermissionDenied {
				t.Fatalf("management grant enabled routing reads: %v", err)
			}
			// No active configuration file is rewritten; only live routing is affected.
			for _, item := range []struct {
				instance settings.Instance
				original []byte
				work     string
			}{{first, configA, workA}, {second, configB, workB}} {
				actual, err := os.ReadFile(item.instance.Xray.ConfigFile)
				if err != nil || !bytes.Equal(actual, item.original) {
					t.Fatalf("native rule application rewrote config: %v", err)
				}
				entries, err := os.ReadDir(item.work)
				if err != nil || len(entries) != 0 {
					t.Fatalf("rule preparation files leaked: %v %v", entries, err)
				}
			}
			if err := xrayTraffic(ctx, healthA, healthB); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func copyRoutingBinaryAndSite(t *testing.T, instance *settings.Instance, domainName string) {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "xray")
	source, err := os.Open(instance.Xray.Binary)
	if err != nil {
		t.Fatal(err)
	}
	target, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o700)
	if err != nil {
		source.Close()
		t.Fatal(err)
	}
	_, copyError := io.Copy(target, source)
	if err := errors.Join(copyError, source.Close(), target.Close()); err != nil {
		t.Fatal(err)
	}
	dataset := &geodata.GeoSiteList{Entry: []*geodata.GeoSite{{Code: "TEST", Domain: []*geodata.Domain{{Type: geodata.Domain_Full, Value: domainName}}}}}
	content, err := proto.Marshal(dataset)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "geosite.dat"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	instance.Xray.Binary = path
}
