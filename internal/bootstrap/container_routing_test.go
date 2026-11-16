//go:build container

package bootstrap_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/geodata"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	xrayv1 "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	routingv1 "github.com/PapilioVPN/node/internal/modules/xray/routing/grpcapi/proto/v1"
	trafficv1 "github.com/PapilioVPN/node/internal/modules/xray/traffic/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/settings"
)

func configureContainerRouting(t *testing.T, cfg *settings.Config, directory string) (string, []byte) {
	t.Helper()
	source, err := os.Open(os.Getenv("XRAY_TEST_BINARY"))
	if err != nil {
		t.Fatalf("provide XRAY_TEST_BINARY or prepare the pinned fixture with make check: %v", err)
	}
	target, err := os.OpenFile(filepath.Join(directory, "xray"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		source.Close()
		t.Fatal(err)
	}
	_, copyError := io.Copy(target, source)
	sourceError, targetError := source.Close(), target.Close()
	if copyError != nil || sourceError != nil || targetError != nil {
		t.Fatalf("copy native fixture: %v %v %v", copyError, sourceError, targetError)
	}
	const native = `{"api":{"tag":"management","listen":"127.0.0.1:10085","services":["RoutingService","ObservatoryService","HandlerService","StatsService"]},"stats":{},"policy":{"system":{"statsInboundUplink":true,"statsInboundDownlink":true}},"inbounds":[{"tag":"users","listen":"127.0.0.1","port":10086,"protocol":"trojan","settings":{"clients":[{"email":"packaged@local","password":"test-only-packaged-account","level":23}]}}],"outbounds":[{"tag":"direct","protocol":"freedom"},{"tag":"blocked","protocol":"blackhole"}],"routing":{"rules":[{"network":"tcp,udp","outboundTag":"direct","ruleTag":"initial"}]},"observatory":{"subjectSelector":["blocked"],"probeURL":"http://fixture.invalid/","probeInterval":"1h"}}`
	if err := os.WriteFile(filepath.Join(directory, "xray.json"), []byte(native), 0o600); err != nil {
		t.Fatal(err)
	}
	dataset, err := proto.Marshal(&geodata.GeoSiteList{Entry: []*geodata.GeoSite{{Code: "TEST", Domain: []*geodata.Domain{{Type: geodata.Domain_Full, Value: "packaged.invalid"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "geosite.dat"), dataset, 0o600); err != nil {
		t.Fatal(err)
	}
	runtimeDirectory := t.TempDir()
	if err := os.Mkdir(filepath.Join(runtimeDirectory, "rules"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg.Instances = []settings.Instance{{ID: "packaged-xray", Engine: instances.Xray, Traffic: &settings.Traffic{ResetConcurrency: 2}, Xray: &settings.Xray{
		Binary: "xray", ConfigFile: "xray.json", StopTimeout: time.Second,
		API:          &settings.XrayAPI{Network: "tcp", Address: "127.0.0.1:10085", RequestTimeout: 2 * time.Second, MaxReceiveBytes: 65536, MaxConcurrent: 2},
		RoutingRules: &settings.RoutingRulePreparation{WorkDir: "/runtime/rules", MaxInputBytes: 4096, MaxOutputBytes: 65536, MaxConcurrent: 1},
	}}}
	return runtimeDirectory, []byte(native)
}

func verifyContainerRouting(t *testing.T, ctx context.Context, client xrayv1.XrayServiceClient, directory, runtimeDirectory string, original []byte) {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	// Only native read readiness is polled. Neither mutating request is replayed.
	for {
		rules, err := client.ListRoutingRules(ctx, &routingv1.ListRoutingRulesRequest{InstanceId: "packaged-xray"})
		if err == nil {
			if len(rules.Rules) != 1 || rules.Rules[0].RuleTag != "initial" {
				t.Fatal("invalid packaged native fixture")
			}
			break
		}
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("packaged native readiness failed: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	reset, err := client.ResetTrafficCounters(ctx, &trafficv1.ResetTrafficCountersRequest{InstanceId: "packaged-xray", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_INBOUND, MaxCounters: 2})
	if err != nil || len(reset.GetItems()) != 2 {
		t.Fatalf("packaged concurrent native reset failed: %v %v", reset, err)
	}
	for _, item := range reset.Items {
		if item.Name != "users" || item.State != trafficv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_ACKNOWLEDGED || item.Acknowledgement == nil || item.Acknowledgement.PreviousBytes != 0 {
			t.Fatalf("packaged reset lost precise acknowledgement: %v", item)
		}
	}
	for _, item := range []struct {
		mode        routingv1.RoutingRuleMode
		tag, target string
		count       int
	}{{routingv1.RoutingRuleMode_ROUTING_RULE_MODE_APPEND, "appended", "direct", 2}, {routingv1.RoutingRuleMode_ROUTING_RULE_MODE_REPLACE, "replacement", "blocked", 1}} {
		rule := []byte(`{"ruleTag":"` + item.tag + `","domain":["geosite:TEST"],"outboundTag":"blocked"}`)
		response, err := client.AddRoutingRule(ctx, &routingv1.AddRoutingRuleRequest{InstanceId: "packaged-xray", RuleJson: rule, Mode: item.mode})
		if err != nil || response.InstanceId != "packaged-xray" || response.RuleTag != item.tag || response.Mode != item.mode || response.AcknowledgedAtUnixMs <= 0 {
			t.Fatalf("packaged native preparation/application failed: %v %v", response, err)
		}
		rules, err := client.ListRoutingRules(ctx, &routingv1.ListRoutingRulesRequest{InstanceId: "packaged-xray"})
		if err != nil || len(rules.Rules) != item.count || rules.Rules[len(rules.Rules)-1].RuleTag != item.tag {
			t.Fatalf("packaged append/replace membership differs: %v %v", rules, err)
		}
		route, err := client.TestRoute(ctx, &routingv1.TestRouteRequest{InstanceId: "packaged-xray", Context: &routingv1.RoutingContext{Network: routingv1.RoutingNetwork_ROUTING_NETWORK_TCP, TargetDomain: "packaged.invalid"}})
		if err != nil || route.GetRoute().GetOutboundTag() != item.target {
			t.Fatalf("packaged actual native routing selection differs: %v %v", route, err)
		}
	}
	_, err = client.AddRoutingRule(ctx, &routingv1.AddRoutingRuleRequest{InstanceId: "packaged-xray", RuleJson: []byte(`{"network":"tcp","outboundTag":"direct"}`)})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("packaged missing mode defaulted to replace: %v", err)
	}
	details := status.Convert(err).Details()
	if len(details) != 1 {
		t.Fatalf("packaged addition outcome missing: %v", err)
	}
	failure, ok := details[0].(*routingv1.RoutingMutationFailure)
	if !ok || failure.Outcome != routingv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED {
		t.Fatalf("packaged addition effects misreported: %v", details)
	}
	_, lost := client.AddRoutingRule(ctx, &routingv1.AddRoutingRuleRequest{InstanceId: "packaged-xray", RuleJson: []byte(`{"ruleTag":"lost","network":"tcp,udp","outboundTag":"direct"}`), Mode: routingv1.RoutingRuleMode_ROUTING_RULE_MODE_REPLACE}, grpc.MaxCallRecvMsgSize(1))
	if status.Code(lost) != codes.ResourceExhausted {
		t.Fatalf("packaged acknowledgement was not discarded: %v", lost)
	}
	rules, err := client.ListRoutingRules(ctx, &routingv1.ListRoutingRulesRequest{InstanceId: "packaged-xray"})
	if err != nil || len(rules.Rules) != 1 || rules.Rules[0].RuleTag != "lost" {
		t.Fatalf("packaged lost reply erased native effect: %v %v", rules, err)
	}
	content, err := os.ReadFile(filepath.Join(directory, "xray.json"))
	if err != nil || !bytes.Equal(content, original) {
		t.Fatalf("packaged mutation rewrote native configuration: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(runtimeDirectory, "rules"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("packaged converter leaked work files: %v %v", entries, err)
	}
}
