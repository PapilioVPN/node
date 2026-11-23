package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"testing"

	routingv1 "github.com/PapilioVPN/node/internal/engines/xray/routing/bridge/grpc/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func verifyRoutingRuleWrites(t *testing.T, ctx context.Context, router http.Handler, client routingv1.XrayRoutingServiceClient, balancer *routingv1.XrayBalancerInfoRequest) {
	t.Helper()
	const appendSource = `{"ruleTag":"added / +","network":"tcp","outboundTag":"pool-a"}`
	const replaceSource = `{"ruleTag":"replaced","network":"tcp","outboundTag":"pool-b"}`
	const replaceAgain = `{"ruleTag":"second replacement","network":"tcp","outboundTag":"pool-a"}`
	const added = "added / +"
	original := []string{"z-first", "", "", "a-last"}
	appended := append(slices.Clone(original), added)
	probe := &routingv1.XrayTestRouteRequest{Id: "primary", Network: "tcp"}

	post := func(body string, code int) {
		t.Helper()
		routingRequest(t, ctx, router, http.MethodPost, base+"/rules", body, code)
	}
	add := func(source string, mode routingv1.XrayRoutingRuleMode, confirmed bool, code codes.Code) {
		t.Helper()
		_, err := client.AddRoutingRule(ctx, &routingv1.XrayAddRoutingRuleRequest{
			Id: "primary", RuleJson: []byte(source), Mode: mode, ConfirmReplace: confirmed,
		})
		if status.Code(err) != code {
			t.Fatalf("gRPC add %s: %v, want %s", source, err, code)
		}
	}
	deleteHTTP := func(tag string, code int) {
		t.Helper()
		routingRequest(t, ctx, router, http.MethodDelete, base+"/rules?tag="+url.QueryEscape(tag), "", code)
	}
	deleteGRPC := func(tag string, code codes.Code) {
		t.Helper()
		_, err := client.RemoveRoutingRule(ctx, &routingv1.XrayRemoveRoutingRuleRequest{Id: "primary", RuleTag: tag})
		if status.Code(err) != code {
			t.Fatalf("gRPC delete %q: %v, want %s", tag, err, code)
		}
	}
	checkRules := func(want []string, outbound string) {
		t.Helper()
		got, err := client.ListRoutingRules(ctx, &routingv1.XrayRoutingRulesRequest{Id: "primary"})
		require(t, err)
		httpResult := new(routingv1.XrayRoutingRules)
		httpBody := routingRequest(t, ctx, router, http.MethodGet, base+"/rules", "", http.StatusOK)
		require(t, json.Unmarshal(httpBody, httpResult))
		for _, rules := range []*routingv1.XrayRoutingRules{got, httpResult} {
			tags := make([]string, 0, len(rules.GetRules()))
			for _, rule := range rules.GetRules() {
				tags = append(tags, rule.GetRuleTag())
			}
			if !slices.Equal(tags, want) {
				t.Fatalf("active tags = %q, want %q", tags, want)
			}
		}
		selected, err := client.TestRoute(ctx, probe)
		require(t, err)
		if selected.GetOutboundTag() != outbound {
			t.Fatalf("route = %v, want %q", selected, outbound)
		}
	}
	setBalancer := func() {
		t.Helper()
		_, err := client.OverrideBalancerTarget(ctx, &routingv1.XrayOverrideBalancerTargetRequest{
			Id: "primary", Tag: balancer.Tag, Target: new("pool-b"),
		})
		require(t, err)
	}

	const appendMode = routingv1.XrayRoutingRuleMode_XRAY_ROUTING_RULE_MODE_APPEND
	const replaceMode = routingv1.XrayRoutingRuleMode_XRAY_ROUTING_RULE_MODE_REPLACE
	post(`{"mode":"replace","rule":`+replaceSource+`}`, http.StatusBadRequest)
	add(replaceSource, replaceMode, false, codes.InvalidArgument)
	checkRules(original, "direct")

	setBalancer()
	post(`{"rule":`+appendSource+`}`, http.StatusNoContent)
	checkRules(appended, "direct")
	info, err := client.GetBalancerInfo(ctx, balancer)
	require(t, err)
	if info.GetOverrideTarget() != "pool-b" {
		t.Fatalf("append discarded the balancer override: %v", info)
	}
	add(appendSource, appendMode, false, codes.AlreadyExists)
	post(`{"rule":`+appendSource+`}`, http.StatusConflict)
	checkRules(appended, "direct")
	deleteGRPC(added, codes.OK)
	deleteGRPC(added, codes.OK)
	checkRules(original, "direct")

	add(appendSource, appendMode, false, codes.OK)
	checkRules(appended, "direct")
	deleteHTTP(added, http.StatusNoContent)
	deleteHTTP(added, http.StatusNoContent)
	deleteHTTP("", http.StatusBadRequest)
	deleteGRPC("", codes.InvalidArgument)
	post(`{"rule":{"outboundTag":"direct","attrs":{"test":"["}}}`, http.StatusBadRequest)
	add(`{"outboundTag":"direct"}`, appendMode, false, codes.InvalidArgument)
	checkRules(original, "direct")

	post(`{"mode":"replace","confirm_replace":true,"rule":`+replaceSource+`}`, http.StatusNoContent)
	checkRules([]string{"replaced"}, "pool-b")
	_, err = client.GetBalancerInfo(ctx, balancer)
	if status.Code(err) != codes.NotFound {
		t.Fatalf("replace left the previous balancer active: %v", err)
	}
	routingRequest(t, ctx, router, http.MethodGet, base+"/balancer?tag="+url.QueryEscape(balancer.Tag), "", http.StatusNotFound)

	add(replaceAgain, replaceMode, true, codes.OK)
	checkRules([]string{"second replacement"}, "pool-a")
}
