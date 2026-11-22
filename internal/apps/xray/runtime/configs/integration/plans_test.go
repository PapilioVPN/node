//go:build integration && linux

package integration_test

import (
	"encoding/json"
	"net"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestRealReviewedNoopLiveTrafficAndStalePlanAcrossTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f, _ := plannedFixture(t, pki, broker, transport)
			c := startNode(t, f)
			awaitTraffic(t, f)
			base := []domain.Source{{Name: "base.json", Content: f.original}}
			exerciseStateReplacement(t, c, transport, base, 0, "1", "2")
			awaitTraffic(t, f)
			before, generation := execution(t, c)
			client, server := connectedTraffic(t, f.proxy)
			payload(t, client, server, "before-noop")
			changed := []domain.Source{{Name: "base.json", Content: append(append([]byte("\n  "), f.original...), '\n')}}
			noop := review(t, c, transport, 1, changed)
			if noop.Mode != "unchanged" || !noop.Retained {
				t.Fatal(noop)
			}
			applied := application(t, c, transport, "apply", noop.ID, 0, "")
			if applied.Error != "" || applied.Outcome != "unchanged" || applied.Effect != "not_required" || applied.Revision != 2 || !applied.ReceiptDurable {
				t.Fatal(applied)
			}
			after, currentGeneration := execution(t, c)
			if before != after || generation != currentGeneration {
				t.Fatal("no-op replaced the process", before, after, generation, currentGeneration)
			}
			awaitTraffic(t, f)
			payload(t, client, server, "after-noop")
			payload(t, client, server, "before-live-change")
			live := review(t, c, transport, 2, routeSource(t, f, "deny"))
			if live.Mode != "live_routing" || !live.Retained || live.Reason != "only_single_rule_routing_changed" {
				t.Fatal(live)
			}
			applied = application(t, c, transport, "apply", live.ID, 0, "")
			if applied.Error != "" || applied.Outcome != "completed" || applied.Readiness != "api_ready" || applied.RequiresRecovery || applied.Revision != 3 {
				t.Fatal(applied)
			}
			after, currentGeneration = execution(t, c)
			if before != after || generation != currentGeneration {
				t.Fatal("live edit replaced the process")
			}
			payload(t, client, server, "after-live-change")
			if err := traffic(f.proxy, f.target.URL); err == nil {
				t.Fatal("new routing was not applied to actual traffic")
			}
			replay := application(t, c, transport, "apply", live.ID, 0, "")
			if replay.Outcome != "completed" || replay.Revision != 3 {
				t.Fatal(replay)
			}
			payload(t, client, server, "after-replay")
			stale := review(t, c, transport, 3, base)
			exerciseStateReplacement(t, c, transport, base, 3, "4", "3")
			awaitTraffic(t, f)
			rejected := application(t, c, transport, "apply", stale.ID, 0, "")
			if rejected.Error != "stale_plan" || rejected.Persistence.Published || rejected.Effect != "not_attempted" {
				t.Fatal(rejected)
			}
			finalID, finalGeneration := execution(t, c)
			if finalID == before || finalGeneration != generation+1 {
				t.Fatal("stale plan changed execution", finalID, finalGeneration)
			}
			if metadata := stateCall(t, c, transport, "status", 0, nil); metadata.Metadata.Revision != "4" {
				t.Fatal(metadata)
			}
		})
	}
}

func TestRealFailedReplacementRecoversPreviousSourcesAfterNodeRestart(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f, _ := plannedFixture(t, pki, broker, transport)
			c := startNode(t, f)
			awaitTraffic(t, f)
			base := []domain.Source{{Name: "base.json", Content: f.original}}
			exerciseStateReplacement(t, c, transport, base, 0, "1", "2")
			awaitTraffic(t, f)
			occupied, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer occupied.Close()
			var bad map[string]any
			if err := json.Unmarshal(f.original, &bad); err != nil {
				t.Fatal(err)
			}
			bad["inbounds"].([]any)[0].(map[string]any)["port"] = occupied.Addr().(*net.TCPAddr).Port
			content, _ := json.Marshal(bad)
			plan := review(t, c, transport, 1, []domain.Source{{Name: "bad.json", Content: content}})
			if plan.Mode != "restart" || plan.Retained {
				t.Fatal(plan)
			}
			failed := application(t, c, transport, "apply", plan.ID, 0, "")
			if failed.Error == "" || !failed.RequiresRecovery || !failed.RecoveryAvailable || !failed.Persistence.Durable || failed.Revision != 2 || !failed.ReceiptDurable {
				t.Fatal(failed)
			}
			status := application(t, c, transport, "get", plan.ID, 0, "")
			if !status.RequiresRecovery || status.ID != plan.ID || status.Revision != 2 {
				t.Fatal(status)
			}
			c.stop()
			recoveredNode := startNode(t, f)
			status = application(t, recoveredNode, transport, "get", plan.ID, 0, "")
			if !status.RequiresRecovery || !status.ReceiptDurable || status.Revision != 2 {
				t.Fatal("lost persisted recovery evidence", status)
			}
			recoveryID := "1234567890abcdef1234567890abcdef"
			recovered := application(t, recoveredNode, transport, "recover", plan.ID, 2, recoveryID)
			if recovered.Recovery == nil || recovered.Recovery.Outcome != "completed" || recovered.Recovery.Readiness != "api_ready" || recovered.Recovery.Revision != 3 || recovered.RequiresRecovery {
				t.Fatal(recovered)
			}
			awaitTraffic(t, f)
			assertDesired(t, stateCall(t, recoveredNode, transport, "get", 0, nil), base, "3")
			before, generation := execution(t, recoveredNode)
			replay := application(t, recoveredNode, transport, "recover", plan.ID, 2, recoveryID)
			after, currentGeneration := execution(t, recoveredNode)
			if replay.Recovery == nil || replay.Recovery.Outcome != "completed" || before != after || generation != currentGeneration {
				t.Fatal("recovery replay restarted the child", replay)
			}
		})
	}
}

func TestRealAppliedRoutingWithoutAcknowledgementHasRecoverableReceipt(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f, native := plannedFixture(t, pki, broker, transport)
			relay := newConfigRelay(t, f, native)
			c := startNode(t, f)
			awaitTraffic(t, f)
			base := []domain.Source{{Name: "base.json", Content: f.original}}
			exerciseStateReplacement(t, c, transport, base, 0, "1", "2")
			awaitTraffic(t, f)
			before, generation := execution(t, c)
			client, server := connectedTraffic(t, f.proxy)
			payload(t, client, server, "before-ack-loss")
			plan := review(t, c, transport, 1, routeSource(t, f, "deny"))
			if plan.Mode != "live_routing" {
				t.Fatal(plan)
			}
			relay.lose.Store(true)
			result := application(t, c, transport, "apply", plan.ID, 0, "")
			if result.Outcome != "unknown" || result.Effect != "unknown" || !result.RequiresRecovery || !result.ReceiptDurable || !result.Persistence.Durable || relay.adds.Load() != 1 {
				t.Fatal(result, relay.adds.Load())
			}
			payload(t, client, server, "after-ack-loss")
			if traffic(f.proxy, f.target.URL) == nil {
				t.Fatal("real engine did not apply routing before ACK loss")
			}
			observed := application(t, c, transport, "get", plan.ID, 0, "")
			replay := application(t, c, transport, "apply", plan.ID, 0, "")
			after, currentGeneration := execution(t, c)
			if observed.Outcome != "unknown" || replay.Outcome != "unknown" || relay.adds.Load() != 1 || before != after || generation != currentGeneration {
				t.Fatal("unknown operation replayed", observed, replay)
			}
			relay.lose.Store(false)
			restored := application(t, c, transport, "recover", plan.ID, 2, "abcdef1234567890abcdef1234567890")
			if restored.Recovery == nil || restored.Recovery.Outcome != "completed" || restored.RequiresRecovery {
				t.Fatal(restored)
			}
			awaitTraffic(t, f)
		})
	}
}
