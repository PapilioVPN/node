//go:build integration && linux

package integration_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestRealXrayCollectionPairsAndResetsAcrossTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f := newFixture(t, pki, strings.Replace(broker.ClientURL(), "nats://", "tls://", 1), transport, fixtureOptions{extraOutbounds: 26, relay: true})
			first := invokeDecode[trafficPage](t, f, transport, "list", map[string]any{"scope": "outbound"})
			if len(first.Counters) != 50 || first.NextPageToken == "" || first.Generation != 1 || first.Source != domain.CollectionSource || f.relay.scans.Load() != 1 {
				t.Fatal(first, f.relay.scans.Load())
			}
			f.sendTraffic(t)
			second := invokeDecode[trafficPage](t, f, transport, "list", map[string]any{"scope": "outbound", "page_token": first.NextPageToken})
			if len(second.Counters) != 4 || second.NextPageToken != "" || second.ObservedAt != first.ObservedAt || f.relay.scans.Load() != 1 {
				t.Fatal(second, f.relay.scans.Load())
			}
			for _, counter := range append(first.Counters, second.Counters...) {
				if counter.Bytes != 0 {
					t.Fatal("continuation refetched", counter)
				}
			}
			for _, scope := range []string{"user", "inbound", "outbound"} {
				name := map[string]string{"user": account, "inbound": inboundTag, "outbound": outboundTag}[scope]
				before := f.relay.gets.Load()
				pair := invokeDecode[trafficPair](t, f, transport, "pair", map[string]any{"scope": scope, "name": name})
				if pair.Error != "" || pair.Uplink == nil || pair.Downlink == nil || pair.Uplink.Bytes <= 0 || pair.Downlink.Bytes <= 0 || f.relay.gets.Load()-before != 2 {
					t.Fatal(pair)
				}
				absent := invokeDecode[trafficPair](t, f, transport, "pair", map[string]any{"scope": scope, "name": "absent"})
				if absent.Error != "" || absent.Uplink != nil || absent.Downlink != nil {
					t.Fatal(absent)
				}
				status := invokeDecode[resetResult](t, f, transport, "reset_status", nil)
				input := resetInput(status, scope, name, "pair-"+scope)
				resets := f.relay.resets.Load()
				report := invokeDecode[resetResult](t, f, transport, "reset_pair", input)
				if report.Outcome != "completed" || report.Total != 2 || len(report.Items) != 2 || f.relay.resets.Load()-resets != 2 {
					t.Fatal(report)
				}
				for _, item := range report.Items {
					if item.Effect != "acknowledged" || item.PreviousBytes == nil || *item.PreviousBytes <= 0 {
						t.Fatal(item)
					}
				}
				// Retrying exactly the original operation returns evidence, with no new swaps.
				duplicate := invokeDecode[resetResult](t, f, transport, "reset_pair", input)
				if duplicate.Revision != report.Revision || f.relay.resets.Load()-resets != 2 {
					t.Fatal("destructive replay", duplicate)
				}
				pair = invokeDecode[trafficPair](t, f, transport, "pair", map[string]any{"scope": scope, "name": name})
				if pair.Uplink == nil || pair.Downlink == nil || pair.Uplink.Bytes != 0 || pair.Downlink.Bytes != 0 {
					t.Fatal("zero lost", pair)
				}
				f.sendTraffic(t)
				input = resetInput(report, scope, name, "exact-"+scope)
				input["direction"] = "uplink"
				scans, resets := f.relay.scans.Load(), f.relay.resets.Load()
				report = invokeDecode[resetResult](t, f, transport, "reset_counter", input)
				if report.Outcome != "completed" || f.relay.resets.Load()-resets != 1 || f.relay.scans.Load() != scans {
					t.Fatal(report)
				}
			}
			// Every exact scope has one scan and N exact swaps. Status pages use no RPC.
			for _, scope := range []string{"user", "inbound", "outbound"} {
				status := invokeDecode[resetResult](t, f, transport, "reset_status", nil)
				input := resetInput(status, scope, "", "scope-"+scope)
				input["max_counters"] = 1000
				scans, resets := f.relay.scans.Load(), f.relay.resets.Load()
				report := invokeDecode[resetResult](t, f, transport, "reset_scope", input)
				expected := 2
				if scope == "outbound" {
					expected = 54
				}
				if report.Outcome != "completed" || report.Total != uint32(expected) || f.relay.scans.Load()-scans != 1 || f.relay.resets.Load()-resets != int64(expected) {
					t.Fatal(report, f.relay.resets.Load()-resets)
				}
				if expected > 50 {
					page := invokeDecode[resetResult](t, f, transport, "reset_status", map[string]any{"revision": fmt.Sprint(report.Revision), "offset": report.NextOffset})
					if len(page.Items) != 4 || page.NextOffset != 0 {
						t.Fatal(page)
					}
				}
			}
		})
	}
}
func TestRealXrayPartialAndLostNativeReplyAcrossTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	for _, transport := range []string{"http", "grpc", "nats"} {
		for _, fault := range []string{"partial", "lost_ack"} {
			t.Run(transport+"/"+fault, func(t *testing.T) {
				f := newFixture(t, pki, strings.Replace(broker.ClientURL(), "nats://", "tls://", 1), transport, fixtureOptions{relay: true})
				f.sendTraffic(t)
				initial := invokeDecode[resetResult](t, f, transport, "reset_status", nil)
				if fault == "partial" {
					f.relay.failAt.Store(2)
				} else {
					f.relay.loseAt.Store(2)
				}
				input := resetInput(initial, "user", account, "fault-"+fault)
				result := invokeDecode[resetResult](t, f, transport, "reset_pair", input)
				want := "partial"
				effect := "not_applied"
				if fault == "lost_ack" {
					want = "unknown"
					effect = "unknown"
				}
				if result.Outcome != want || len(result.Items) != 2 || result.Items[0].Effect != "acknowledged" || result.Items[1].Effect != effect || f.relay.resets.Load() != 2 {
					t.Fatal(result, f.relay.resets.Load())
				}
				uplink := f.read(t, transport, domain.Selection{Scope: domain.User, Name: account, Direction: domain.Uplink})
				downlink := f.read(t, transport, domain.Selection{Scope: domain.User, Name: account, Direction: domain.Downlink})
				if uplink.Bytes != 0 || (fault == "partial" && downlink.Bytes <= 0) || (fault == "lost_ack" && downlink.Bytes != 0) {
					t.Fatal("actual effect differs", uplink, downlink)
				}
				status := invokeDecode[resetResult](t, f, transport, "reset_status", map[string]any{"revision": fmt.Sprint(result.Revision)})
				if status.OperationID != result.OperationID || status.Outcome != want {
					t.Fatal(status)
				}
				duplicate := invokeDecode[resetResult](t, f, transport, "reset_pair", input)
				if duplicate.Revision != result.Revision || f.relay.resets.Load() != 2 {
					t.Fatal("ambiguous replay", duplicate)
				}
				if fault == "lost_ack" {
					next := resetInput(status, "user", account, "unsafe-retry")
					rejected := invokeDecode[resetResult](t, f, transport, "reset_pair", next)
					if rejected.Error != "restart_required" || f.relay.resets.Load() != 2 {
						t.Fatal("unknown execution not fenced", rejected)
					}
				}
			})
		}
	}
}

func TestRealXrayThousandCountersUseOneScanForTraversal(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f := newFixture(t, pki, strings.Replace(broker.ClientURL(), "nats://", "tls://", 1), transport, fixtureOptions{extraOutbounds: 499, relay: true})
			total, pages := 0, 0
			token := ""
			for {
				input := map[string]any{"scope": "outbound"}
				if token != "" {
					input["page_token"] = token
				}
				page := invokeDecode[trafficPage](t, f, transport, "list", input)
				if page.Error != "" || len(page.Counters) > 50 {
					t.Fatal(page)
				}
				total += len(page.Counters)
				pages++
				token = page.NextPageToken
				if token == "" {
					break
				}
			}
			if total != 1000 || pages != 20 || f.relay.scans.Load() != 1 || f.relay.gets.Load() != 0 {
				t.Fatal(total, pages, f.relay.scans.Load(), f.relay.gets.Load())
			}
			status := invokeDecode[resetResult](t, f, transport, "reset_status", nil)
			input := resetInput(status, "outbound", "", "thousand-reset")
			input["max_counters"] = 1000
			result := invokeDecode[resetResult](t, f, transport, "reset_scope", input)
			if result.Outcome != "completed" || result.Total != 1000 || len(result.Items) != 50 || f.relay.resets.Load() != 1000 || f.relay.scans.Load() != 2 {
				t.Fatal(result, f.relay.resets.Load(), f.relay.scans.Load())
			}
			total = len(result.Items)
			for result.NextOffset > 0 {
				result = invokeDecode[resetResult](t, f, transport, "reset_status", map[string]any{"revision": fmt.Sprint(result.Revision), "offset": result.NextOffset})
				if result.Error != "" || len(result.Items) > 50 {
					t.Fatal(result)
				}
				total += len(result.Items)
			}
			if total != 1000 || f.relay.resets.Load() != 1000 {
				t.Fatal("status replayed or truncated", total)
			}
			t.Log("1000 counters: 20 pages, 1 QueryStats for traversal; scope reset 1 QueryStats + 1000 GetStats; status no native calls")
		})
	}
}

func TestRealXrayScopedResetStopsWithOrderedTail(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	for _, transport := range []string{"http", "grpc", "nats"} {
		for _, fault := range []string{"partial", "lost_ack"} {
			t.Run(transport+"/"+fault, func(t *testing.T) {
				f := newFixture(t, pki, strings.Replace(broker.ClientURL(), "nats://", "tls://", 1), transport, fixtureOptions{extraOutbounds: 2, trailingOutbounds: true, relay: true})
				f.sendTraffic(t)
				initial := invokeDecode[resetResult](t, f, transport, "reset_status", nil)
				if fault == "partial" {
					f.relay.failAt.Store(2)
				} else {
					f.relay.loseAt.Store(2)
				}
				input := resetInput(initial, "outbound", "", "scope-fault-"+fault)
				input["max_counters"] = 100
				result := invokeDecode[resetResult](t, f, transport, "reset_scope", input)
				outcome, effect := "partial", "not_applied"
				if fault == "lost_ack" {
					outcome, effect = "unknown", "unknown"
				}
				if result.Outcome != outcome || result.Total != 6 || len(result.Items) != 6 || result.Items[0].Effect != "acknowledged" || result.Items[0].PreviousBytes == nil || *result.Items[0].PreviousBytes <= 0 || result.Items[1].Effect != effect || f.relay.resets.Load() != 2 || f.relay.scans.Load() != 1 {
					t.Fatal(result)
				}
				for _, item := range result.Items[2:] {
					if item.Attempted || item.Effect != "not_attempted" || item.PreviousBytes != nil {
						t.Fatal("tail was attempted", item)
					}
				}
				uplink := f.read(t, transport, domain.Selection{Scope: domain.Outbound, Name: outboundTag, Direction: domain.Uplink})
				downlink := f.read(t, transport, domain.Selection{Scope: domain.Outbound, Name: outboundTag, Direction: domain.Downlink})
				if downlink.Bytes != 0 || (fault == "partial" && uplink.Bytes <= 0) || (fault == "lost_ack" && uplink.Bytes != 0) {
					t.Fatal("scope effects differ", uplink, downlink)
				}
				duplicate := invokeDecode[resetResult](t, f, transport, "reset_scope", input)
				if duplicate.Revision != result.Revision || f.relay.resets.Load() != 2 || f.relay.scans.Load() != 1 {
					t.Fatal("scope replayed", duplicate)
				}
			})
		}
	}
}
