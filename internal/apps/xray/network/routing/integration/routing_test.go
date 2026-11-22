//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/testutil"
	native "github.com/xtls/xray-core/app/router/command"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

func TestRealRoutingAcrossIndependentTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	brokerURL := strings.Replace(broker.ClientURL(), "nats://", "tls://", 1)
	for _, network := range []string{"tcp", "unix"} {
		for _, transport := range []string{"http", "grpc", "nats"} {
			t.Run(network+"/"+transport, func(t *testing.T) {
				f := newFixture(t, pki, brokerURL, transport, network, 0, "")
				initial := f.invoke(t, transport, "list", nil)
				requireSuccess(t, initial)
				if len(initial["rules"].([]any)) != 2 {
					t.Fatal(initial)
				}
				f.traffic(t, true)
				full := f.invoke(t, transport, "probe", map[string]any{"context": map[string]any{"inbound_tag": "proxy", "network": 2, "source_ips": []string{"::ffff:127.0.0.2"}, "target_ips": []string{"127.0.0.1"}, "local_ips": []string{"::1"}, "source_port": 65533, "target_port": 65535, "local_port": 65534, "target_domain": "typed.test", "protocol": "http", "user": "opaque>>>user", "attributes": map[string]string{"key": "value"}, "outbound_tag": "caller-input", "vless_route": 65535}, "field_selectors": []string{""}})
				requireSuccess(t, full)
				observed := full["route"].(map[string]any)
				if observed["inbound_tag"] != "proxy" || observed["source_port"] != float64(65533) || observed["local_port"] != float64(65534) || observed["target_domain"] != "typed.test" || observed["protocol"] != "http" || observed["user"] != "opaque>>>user" || observed["attributes"].(map[string]any)["key"] != "value" || observed["source_ips"].([]any)[0] != "::ffff:127.0.0.2" || observed["local_ips"].([]any)[0] != "::1" || observed["vless_route"] != float64(0) || observed["outbound_tag"] == "caller-input" {
					t.Fatal("typed native context lost", full)
				}
				tunnel := f.openTunnel(t)
				tunnel()
				first := route(t, f, transport, "proxy")
				second := route(t, f, transport, "proxy")
				if first == second || !strings.HasPrefix(first, "direct-") || !strings.HasPrefix(second, "direct-") {
					t.Fatal("round-robin did not advance", first, second)
				}
				b := f.invoke(t, transport, "balancer", map[string]any{"tag": "b"})
				requireSuccess(t, b)
				if b["override"] != "" || b["principal_targets"] == nil {
					t.Fatal("balancer presence lost", b)
				}
				if got := route(t, f, transport, "proxy"); got != first {
					t.Fatal("round-robin order changed", got, first)
				}
				requireSuccess(t, f.invoke(t, transport, "override", map[string]any{"balancer_tag": "b", "target": "block"}))
				if got := route(t, f, transport, "proxy"); got != "block" {
					t.Fatal(got)
				}
				f.traffic(t, false)
				tunnel()
				absent := f.invoke(t, transport, "override", map[string]any{"balancer_tag": "b"})
				if absent["error"] != "invalid_argument" {
					t.Fatal("omitted target cleared override", absent)
				}
				if got := route(t, f, transport, "proxy"); got != "block" {
					t.Fatal(got)
				}
				requireSuccess(t, f.invoke(t, transport, "override", map[string]any{"balancer_tag": "b", "target": ""}))
				if got := route(t, f, transport, "proxy"); got != second {
					t.Fatal("clearing override reset strategy index", got, second)
				}
				requireSuccess(t, f.invoke(t, transport, "override", map[string]any{"balancer_tag": "b", "target": "block"}))
				requireSuccess(t, f.invoke(t, transport, "override", map[string]any{"balancer_tag": "b", "target": ""}))
				if got := route(t, f, transport, "proxy"); got != first {
					t.Fatal("second clear reset strategy index", got, first)
				}
				f.traffic(t, true)
				// An override outside selectors and even without an actual outbound is
				// accepted natively. Route choice still cannot establish network success.
				requireSuccess(t, f.invoke(t, transport, "override", map[string]any{"balancer_tag": "b", "target": "nonexistent"}))
				if got := route(t, f, transport, "proxy"); got != "nonexistent" {
					t.Fatal(got)
				}
				requireSuccess(t, f.invoke(t, transport, "override", map[string]any{"balancer_tag": "b", "target": ""}))
				projection := f.invoke(t, transport, "probe", map[string]any{"context": map[string]any{"inbound_tag": "proxy", "target_domain": "private.test", "source_ips": []string{"::ffff:127.0.0.2", "::ffff:127.0.0.2"}, "target_port": 65535, "vless_route": 65535}, "field_selectors": []string{"ip_source", "port", "unknown"}, "publish_result": true})
				requireSuccess(t, projection)
				selected := projection["route"].(map[string]any)
				if selected["outbound_tag"] != "" || selected["target_port"] != float64(65535) || selected["vless_route"] != float64(0) || len(selected["source_ips"].([]any)) != 2 {
					t.Fatal("native projection changed", projection)
				}
				rule := map[string]any{"type": "field", "ruleTag": "new", "inboundTag": []string{"special"}, "vlessRoute": "65535", "outboundTag": "block"}
				requireSuccess(t, f.invoke(t, transport, "add", map[string]any{"rule_json": rule, "mode": "append"}))
				probe := f.invoke(t, transport, "probe", map[string]any{"context": map[string]any{"inbound_tag": "special", "vless_route": 65535}, "field_selectors": []string{"outbound"}})
				requireSuccess(t, probe)
				if probe["route"].(map[string]any)["outbound_tag"] != "block" {
					t.Fatal(probe)
				}
				duplicate := f.invoke(t, transport, "add", map[string]any{"rule_json": rule, "mode": "append"})
				if duplicate["error"] == nil || duplicate["operation"].(map[string]any)["effect"] != "unknown" {
					t.Fatal("native error claimed rollback", duplicate)
				}
				requireSuccess(t, f.invoke(t, transport, "remove", map[string]any{"tag": "new"}))
				requireSuccess(t, f.invoke(t, transport, "remove", map[string]any{"tag": "missing"}))
				// Anonymous and whitespace rule tags retain native membership semantics.
				for _, tag := range []string{"", " "} {
					requireSuccess(t, f.invoke(t, transport, "add", map[string]any{"mode": "append", "rule_json": map[string]any{"type": "field", "ruleTag": tag, "inboundTag": []string{"anonymous"}, "outboundTag": "direct-a"}}))
				}
				requireSuccess(t, f.invoke(t, transport, "remove", map[string]any{"tag": " "}))
				emptyRemove := f.invoke(t, transport, "remove", map[string]any{"tag": ""})
				if emptyRemove["error"] != "invalid_argument" {
					t.Fatal(emptyRemove)
				}
				rejected := f.invoke(t, transport, "add", map[string]any{"mode": "append", "rule_json": map[string]any{"type": "field", "ruleTag": "bad-ip", "ip": []string{"invalid-cidr"}, "outboundTag": "block"}})
				if rejected["error"] != "invalid_argument" || rejected["operation"].(map[string]any)["effect"] != "not_applied" {
					t.Fatal("preflight rejection dispatched mutation", rejected)
				}
				// Builder acceptance does not imply runtime condition acceptance.
				invalid := f.invoke(t, transport, "add", map[string]any{"rule_json": map[string]any{"type": "field", "ruleTag": "bad", "domain": []string{"regexp:["}, "outboundTag": "block"}, "mode": "append"})
				if invalid["operation"].(map[string]any)["effect"] != "unknown" {
					t.Fatal(invalid)
				}
				status := f.invoke(t, transport, "status", nil)
				requireSuccess(t, status)
				if status["requires_instance_restart"] != false || status["routing_stats_stream"] != "disabled_stock_xray" {
					t.Fatal(status)
				}
				replacement := map[string]any{"type": "field", "ruleTag": "replacement", "inboundTag": []string{"proxy"}, "outboundTag": "block"}
				denied := f.invoke(t, transport, "add", map[string]any{"rule_json": replacement, "mode": "replace"})
				if denied["error"] != "invalid_argument" {
					t.Fatal("destructive replacement had no confirmation", denied)
				}
				requireSuccess(t, f.invoke(t, transport, "add", map[string]any{"rule_json": replacement, "mode": "replace", "confirm_destructive": true}))
				if got := route(t, f, transport, "proxy"); got != "block" {
					t.Fatal(got)
				}
				f.traffic(t, false)
				tunnel()
				removedBalancer := f.invoke(t, transport, "balancer", map[string]any{"tag": "b"})
				if removedBalancer["error"] == nil {
					t.Fatal("replace retained balancer", removedBalancer)
				}
				remaining := f.invoke(t, transport, "list", nil)
				requireSuccess(t, remaining)
				rows := remaining["rules"].([]any)
				if len(rows) != 1 || rows[0].(map[string]any)["rule_tag"] != "replacement" {
					t.Fatal(remaining)
				}
				other := f.invoke(t, transport, "list", map[string]any{"instance_id": "edge-b"})
				requireSuccess(t, other)
				if len(other["rules"].([]any)) != 2 {
					t.Fatal("other instance changed", other)
				}
				requireSuccess(t, f.invoke(t, transport, "remove", map[string]any{"tag": "replacement"}))
				f.traffic(t, true)
				// The official standalone service declares the stream but has no source.
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				stream, err := f.native.SubscribeRoutingStats(ctx, &native.SubscribeRoutingStatsRequest{})
				if err == nil {
					_, err = stream.Recv()
				}
				if grpcstatus.Code(err) != codes.Unknown || !strings.Contains(grpcstatus.Convert(err).Message(), "Routing statistics not enabled") {
					t.Fatal("unexpected native stream outcome", err)
				}
				t.Logf("%s/%s: route selection, real allowed/blocked payload, append/remove/replace, override/clear, isolation and disabled native stream verified", network, transport)
			})
		}
	}
}
func TestRealRulePaginationIsOneObservation(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	f := newFixture(t, pki, strings.Replace(broker.ClientURL(), "nats://", "tls://", 1), "all", "tcp", 49, "")
	for _, transport := range []string{"http", "grpc", "nats"} {
		first := f.invoke(t, transport, "list", nil)
		requireSuccess(t, first)
		if len(first["rules"].([]any)) != 50 || first["total"] != float64(51) {
			t.Fatal(first)
		}
		token := first["next_page_token"].(string)
		// Mutate after the first page; continuation must retain the old priority
		// observation rather than silently re-fetching a different list.
		requireSuccess(t, f.invoke(t, transport, "remove", map[string]any{"tag": "rule-0048"}))
		next := f.invoke(t, transport, "list", map[string]any{"page_token": token})
		requireSuccess(t, next)
		if next["fresh"] != false || next["observation_id"] != first["observation_id"] || len(next["rules"].([]any)) != 1 {
			t.Fatal(next)
		}
		// Put the removed rule back to restore a 51-record fixture for the next
		// transport, preserving native order for that transport's new observation.
		requireSuccess(t, f.invoke(t, transport, "add", map[string]any{"mode": "append", "rule_json": map[string]any{"type": "field", "ruleTag": "rule-0048", "inboundTag": []string{"unrelated-48"}, "outboundTag": "direct-a"}}))
	}
	f.traffic(t, true)
	encoded, _ := json.Marshal(f.invoke(t, "http", "status", nil))
	t.Log(string(encoded))
}

func TestThousandNativeRulesAndDisabledAPIAcrossTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	brokerURL := strings.Replace(broker.ClientURL(), "nats://", "tls://", 1)
	t.Run("1000-rules", func(t *testing.T) {
		f := newFixture(t, pki, brokerURL, "all", "tcp", 998, "")
		for _, transport := range []string{"http", "grpc", "nats"} {
			token := ""
			count := 0
			observation := ""
			for {
				input := map[string]any{}
				if token != "" {
					input["page_token"] = token
				}
				page := f.invoke(t, transport, "list", input)
				requireSuccess(t, page)
				if observation != "" && page["observation_id"] != observation {
					t.Fatal("observation changed")
				}
				observation = page["observation_id"].(string)
				count += len(page["rules"].([]any))
				token = page["next_page_token"].(string)
				if token == "" {
					break
				}
			}
			if count != 1000 {
				t.Fatal(transport, count)
			}
		}
		f.traffic(t, true)
	})
	t.Run("no-api", func(t *testing.T) {
		f := newFixture(t, pki, brokerURL, "all", "tcp", 0, "", func(c *settings.Config) { c.Instances[0].Xray.API = nil })
		for _, transport := range []string{"http", "grpc", "nats"} {
			list := f.invoke(t, transport, "list", nil)
			if list["error"] != "failed_precondition" {
				t.Fatal(list)
			}
			result := f.invoke(t, transport, "override", map[string]any{"balancer_tag": "b", "target": "block"})
			if result["error"] != "failed_precondition" || result["operation"].(map[string]any)["effect"] != "not_applied" {
				t.Fatal(result)
			}
			status := f.invoke(t, transport, "status", nil)
			requireSuccess(t, status)
			if status["api_configured"] != false {
				t.Fatal(status)
			}
		}
		f.traffic(t, true)
	})
}
