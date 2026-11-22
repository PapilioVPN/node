//go:build integration && linux

package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/testutil"
	native "github.com/xtls/xray-core/app/router/command"
)

func TestLostNativeAcknowledgementFencesAllTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	brokerURL := strings.Replace(broker.ClientURL(), "nats://", "tls://", 1)
	for _, origin := range []string{"http", "grpc", "nats"} {
		t.Run(origin, func(t *testing.T) {
			var drop *atomic.Bool
			f := newFixture(t, pki, brokerURL, "all", "tcp", 0, "", func(c *settings.Config) {
				relay, d := testutil.LostReplies(t, c.Instances[0].Xray.API.Address)
				drop = d
				c.Instances[0].Xray.API.Address = relay
				c.Communications.Requests.Timeout = 300 * time.Millisecond
			})
			result := f.invoke(t, origin, "override", map[string]any{"balancer_tag": "b", "target": "block"})
			if result["error"] != "deadline_exceeded" {
				t.Fatal(result)
			}
			o := result["operation"].(map[string]any)
			if o["effect"] != "unknown" || o["acknowledged"] != false || o["requires_instance_restart"] != true {
				t.Fatal(result)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			actual, err := f.native.GetBalancerInfo(ctx, &native.GetBalancerInfoRequest{Tag: "b"})
			if err != nil || actual.Balancer.Override.Target != "block" {
				t.Fatal("lost request was not really applied", actual, err)
			}
			f.traffic(t, false)
			drop.Store(false)
			assertFence(t, f)
			// Explicit lifecycle replacement is the recovery action. No routing request
			// replays the ambiguous assignment or silently edits desired configuration.
			ctx, restartCancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer restartCancel()
			if _, err := f.restarter.Restart(ctx, "edge-a"); err != nil {
				t.Fatal(err)
			}
			awaitNative(t, "tcp", strings.TrimPrefix(f.nativeConn.Target(), "passthrough:///"))
			deadline := time.Now().Add(4 * time.Second)
			for {
				observed := f.invoke(t, origin, "list", nil)
				if observed["error"] == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("owned native channel did not reconnect", observed)
				}
				time.Sleep(50 * time.Millisecond)
			}
			status := f.invoke(t, "http", "status", nil)
			requireSuccess(t, status)
			if status["requires_instance_restart"] != false || status["observation"].(map[string]any)["generation"] != "2" || status["last_operation"].(map[string]any)["generation"] != "1" {
				t.Fatal(status)
			}
			f.traffic(t, true)
			requireSuccess(t, f.invoke(t, origin, "override", map[string]any{"balancer_tag": "b", "target": ""}))
			t.Logf("%s lost native ACK: actual override applied, all transports fenced, generation 2 recovered", origin)
		})
	}
}
func assertFence(t *testing.T, f *fixture) {
	t.Helper()
	for _, transport := range []string{"http", "grpc", "nats"} {
		status := f.invoke(t, transport, "status", nil)
		requireSuccess(t, status)
		if status["requires_instance_restart"] != true || status["last_operation"].(map[string]any)["effect"] != "unknown" {
			t.Fatal(status)
		}
		retry := f.invoke(t, transport, "override", map[string]any{"balancer_tag": "b", "target": ""})
		if retry["error"] != "restart_required" || retry["operation"].(map[string]any)["effect"] != "not_applied" {
			t.Fatal("ambiguous mutation was replayed", retry)
		}
		list := f.invoke(t, transport, "list", nil)
		if list["error"] != "restart_required" {
			t.Fatal("current state was read through uncertainty", list)
		}
	}
}
func TestDestructiveReplacementIsVisibleBeforeBlockedNativeCleanup(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	brokerURL := strings.Replace(broker.ClientURL(), "nats://", "tls://", 1)
	for _, origin := range []string{"http", "grpc", "nats"} {
		t.Run(origin, func(t *testing.T) {
			entered, release := make(chan struct{}, 1), make(chan struct{})
			var released atomic.Bool
			webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case entered <- struct{}{}:
				default:
				}
				<-release
				fmt.Fprint(w, "ok")
			}))
			defer webhook.Close()
			defer func() {
				if released.CompareAndSwap(false, true) {
					close(release)
				}
			}()
			f := newFixture(t, pki, brokerURL, "all", "tcp", 0, webhook.URL, func(c *settings.Config) { c.Communications.Requests.Timeout = 300 * time.Millisecond })
			if route(t, f, "http", "proxy") == "" {
				t.Fatal("missing selection")
			}
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("native webhook did not fire with publish_result=false")
			}
			result := f.invoke(t, origin, "add", map[string]any{"mode": "replace", "confirm_destructive": true, "rule_json": map[string]any{"type": "field", "ruleTag": "replacement", "inboundTag": []string{"proxy"}, "outboundTag": "block"}})
			if result["error"] != "deadline_exceeded" || result["operation"].(map[string]any)["effect"] != "unknown" {
				t.Fatal(result)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			actual, err := f.native.ListRule(ctx, &native.ListRuleRequest{})
			if err != nil || len(actual.Rules) != 1 || actual.Rules[0].RuleTag != "replacement" {
				t.Fatal("replacement was not visible before old cleanup", actual, err)
			}
			if _, err := f.native.GetBalancerInfo(ctx, &native.GetBalancerInfoRequest{Tag: "b"}); err == nil {
				t.Fatal("old balancer survived destructive replacement")
			}
			f.traffic(t, false)
			assertFence(t, f)
			released.Store(true)
			close(release)
			// RemoveRule takes the same native mutation mutex, so its completion proves
			// the previously blocked cleanup has returned. This is test observation only.
			if _, err := f.native.RemoveRule(ctx, &native.RemoveRuleRequest{RuleTag: "missing"}); err != nil {
				t.Fatal(err)
			}
			assertFence(t, f)
			t.Logf("%s replacement: rules and balancers changed while native cleanup blocked; late completion did not clear fence", origin)
		})
	}
}
