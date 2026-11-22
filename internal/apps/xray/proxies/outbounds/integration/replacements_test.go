//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/testutil"
	handlers "github.com/xtls/xray-core/app/proxyman/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestRealReplacementModesEffectsAndReplayAcrossTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		for _, defaults := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/default=%t", transport, defaults), func(t *testing.T) {
				target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "outbound-traffic") }))
				defer target.Close()
				streamTarget := tunnelTarget(t)
				f := newFixture(t, pki, broker, transport)
				proxyAddress := configureProbe(t, f, !defaults)
				c := startNode(t, f)
				awaitNative(t, f.native)
				tag := "baseline"
				if !defaults {
					tag = "managed"
					if result, code := nativeMutation(t, c, transport, "add", "", candidate(t, tag, "freedom")); code != "" || !result.Acknowledged {
						t.Fatal(result, code)
					}
				}
				proxy, _ := url.Parse("http://" + proxyAddress)
				tr := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
				defer tr.CloseIdleConnections()
				traffic := &http.Client{Transport: tr, Timeout: time.Second}
				requireTraffic(t, traffic, target.URL, true)
				stream, upstream := tunnel(t, proxyAddress, streamTarget)
				tunnelPayload(t, stream, upstream, "before-replacement")
				restart := planReplacement(t, c, transport, tag, candidate(t, tag, "blackhole"), "preserve", "retain")
				if restart.Error != "" || restart.Mode != "configs_restart" || restart.DefaultState != "unobservable" || restart.ExistingConnectionsRetained || restart.NativeResourcesMayRemain {
					t.Fatal(restart)
				}
				rejected := applyReplacement(t, c, transport, restart.ID)
				if rejected.Outcome != "not_applied" || rejected.Error != "configs_restart_required" || rejected.Steps[0].Attempted {
					t.Fatal(rejected)
				}
				requireTraffic(t, traffic, target.URL, true)
				plan := planReplacement(t, c, transport, tag, candidate(t, tag, "blackhole"), "may_change", "retain")
				if plan.Mode != "live_remove_add" || !plan.ExistingConnectionsRetained || !plan.NativeResourcesMayRemain {
					t.Fatal(plan)
				}
				result := applyReplacement(t, c, transport, plan.ID)
				if result.Outcome != "completed" || len(result.Steps) != 2 || !result.Steps[0].Acknowledged || !result.Steps[1].Acknowledged || !result.NativeResourcesMayRemain {
					t.Fatal(result)
				}
				requireTraffic(t, traffic, target.URL, false)
				tunnelPayload(t, stream, upstream, "after-replacement")
				// External changes after acknowledgement survive replay of the consumed plan.
				nativeMutation(t, c, transport, "remove", tag, nil)
				nativeMutation(t, c, transport, "add", "", candidate(t, tag, "freedom"))
				requireTraffic(t, traffic, target.URL, true)
				again := applyReplacement(t, c, transport, plan.ID)
				if again.Outcome != "completed" || again.ID != result.ID {
					t.Fatal(again)
				}
				requireTraffic(t, traffic, target.URL, true)
				closePlan := planReplacement(t, c, transport, tag, candidate(t, tag, "freedom"), "may_change", "terminate")
				if closePlan.Mode != "configs_restart" {
					t.Fatal(closePlan)
				}
			})
		}
	}
}
func TestRealReplacementFailureLeavesRemovalAndReportsEachStep(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "outbound-traffic") }))
			defer target.Close()
			streamTarget := tunnelTarget(t)
			f := newFixture(t, pki, broker, transport)
			address := configureProbe(t, f, false)
			c := startNode(t, f)
			awaitNative(t, f.native)
			proxy, _ := url.Parse("http://" + address)
			tr := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
			defer tr.CloseIdleConnections()
			traffic := &http.Client{Transport: tr, Timeout: time.Second}
			requireTraffic(t, traffic, target.URL, true)
			stream, upstream := tunnel(t, address, streamTarget)
			tunnelPayload(t, stream, upstream, "before-failure")
			plan := planReplacement(t, c, transport, "baseline", candidate(t, "baseline", "bad"), "may_change", "retain")
			if plan.Error != "" || plan.Mode != "live_remove_add" {
				t.Fatal(plan)
			}
			result := applyReplacement(t, c, transport, plan.ID)
			if result.Outcome != "unknown" || len(result.Steps) != 2 || result.Steps[0].Effect != "completed" || !result.Steps[0].Acknowledged || result.Steps[1].Effect != "unknown" || result.Steps[1].Acknowledged {
				t.Fatal(result)
			}
			tunnelPayload(t, stream, upstream, "after-failure")
			// Missing default does not promote the already installed freedom backup.
			requireTraffic(t, traffic, target.URL, false)
			records := readOutbounds(t, c, transport)
			if len(records.Outbounds) != 1 || records.Outbounds[0].Tag != "backup" {
				t.Fatal(records)
			}
			nativeMutation(t, c, transport, "add", "", candidate(t, "baseline", "freedom"))
			requireTraffic(t, traffic, target.URL, true)
			again := applyReplacement(t, c, transport, plan.ID)
			if again.Outcome != "unknown" {
				t.Fatal(again)
			}
			requireTraffic(t, traffic, target.URL, true)
		})
	}
}

func TestRealReplacementLostAddAcknowledgementHasReadableConsumedPlan(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f := newFixture(t, pki, broker, transport)
			relay := newRelay(t, &f)
			c := startNode(t, f)
			awaitNative(t, f.native)
			plan := planReplacement(t, c, transport, "baseline", candidate(t, "baseline", "freedom"), "may_change", "retain")
			if plan.Mode != "live_remove_add" {
				t.Fatal(plan)
			}
			relay.loseAdd.Store(true)
			result := applyReplacement(t, c, transport, plan.ID)
			if result.Outcome != "unknown" || !result.RequiresInstanceRestart || result.Steps[0].Effect != "completed" || result.Steps[1].Effect != "unknown" || relay.adds.Load() != 1 || relay.removes.Load() != 1 || relay.lists.Load() != 2 {
				t.Fatal(result)
			}
			data := replacementCall(t, c, transport, "get", map[string]any{"plan_id": plan.ID})
			var status replacementResult
			if err := json.Unmarshal(data, &status); err != nil || status.Outcome != "unknown" || status.ID != plan.ID {
				t.Fatal(status, err)
			}
			again := applyReplacement(t, c, transport, plan.ID)
			if again.Outcome != "unknown" || relay.adds.Load() != 1 || relay.removes.Load() != 1 {
				t.Fatal("lost acknowledgement replayed", again)
			}
			direct, err := grpc.NewClient(f.native, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			defer direct.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			native, err := handlers.NewHandlerServiceClient(direct).ListOutbounds(ctx, &handlers.ListOutboundsRequest{})
			if err != nil || len(native.Outbounds) != 1 || native.Outbounds[0].ProxySettings.Type != "xray.proxy.freedom.Config" {
				t.Fatal("real add was not applied", native, err)
			}
		})
	}
}

func TestRealReplacementSourcePlanSelectsOneOutboundWithoutPreflightEffects(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f := newFixture(t, pki, broker, transport)
			f.config.Instances[0].Xray.WorkDirectory = t.TempDir()
			relay := newRelay(t, &f)
			c := startNode(t, f)
			awaitNative(t, f.native)
			input := map[string]any{"tag": "baseline", "source": []byte(`{"outbounds":[{"tag":"baseline","protocol":"freedom"},{"tag":"unselected","protocol":"blackhole"}]}`), "default_policy": "may_change", "connection_policy": "retain"}
			data := replacementCall(t, c, transport, "plan", input)
			var plan replacementPlan
			if err := json.Unmarshal(data, &plan); err != nil || plan.Error != "" || plan.Mode != "live_remove_add" {
				t.Fatal(string(data), err)
			}
			if relay.adds.Load() != 0 || relay.removes.Load() != 0 || relay.lists.Load() != 1 {
				t.Fatal("planning changed engine membership")
			}
			result := applyReplacement(t, c, transport, plan.ID)
			if result.Outcome != "completed" || relay.lists.Load() != 2 || relay.adds.Load() != 1 || relay.removes.Load() != 1 {
				t.Fatal(result)
			}
			records := readOutbounds(t, c, transport)
			if len(records.Outbounds) != 1 || records.Outbounds[0].Tag != "baseline" {
				t.Fatal(records)
			}
		})
	}
}
