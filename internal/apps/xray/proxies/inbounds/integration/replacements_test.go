//go:build integration && linux

package integration_test

import (
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/testutil"
)

func TestNativeReplacementContinuityFailureAndConfigFallbackOverAllTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			target := reserveTCP(t)
			old := reserveTCP(t)
			oldAddress := old.Addr().String()
			oldPort := old.Addr().(*net.TCPAddr).Port
			f := newFixture(t, pki, broker, transport)
			relayAddress, relay := replacementRelay(t, f.native)
			f.config.Instances[0].Xray.API.Address = relayAddress
			f.config.Instances[0].Xray.WorkDirectory = t.TempDir()
			f.config.Instances[0].Xray.StateDirectory = testutil.NewPrivateDirectory(t)
			f.config.Communications.Requests.Timeout = 3 * time.Second
			if f.config.Communications.HTTP != nil {
				f.config.Communications.HTTP.WriteTimeout = 4 * time.Second
			}
			source, err := os.ReadFile(f.config.Instances[0].Xray.Configuration)
			if err != nil {
				t.Fatal(err)
			}
			var config map[string]any
			if err := json.Unmarshal(source, &config); err != nil {
				t.Fatal(err)
			}
			config["inbounds"] = []any{map[string]any{"tag": "tunnel", "listen": "127.0.0.1", "port": oldPort, "protocol": "http", "settings": map[string]any{}}}
			source, err = json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.config.Instances[0].Xray.Configuration, source, 0600); err != nil {
				t.Fatal(err)
			}
			old.Close()
			c := startNode(t, f)
			awaitNative(t, f.native)
			transferOld, oldClosed := openTunnel(t, oldAddress, target)
			transferOld("before")
			next := reserveTCP(t)
			nextAddress := next.Addr().String()
			candidate := handlerBytes(t, uint32(next.Addr().(*net.TCPAddr).Port))
			next.Close()
			restart := planInbound(t, c, transport, candidate, true)
			if restart.Mode != "restart" || restart.Reason != "disconnect_existing_connections" || restart.Fallback != "replace_configuration" {
				t.Fatal(restart)
			}
			transferOld("after-read-only-restart-plan")
			p := planInbound(t, c, transport, candidate, false)
			if p.Mode != "live" || p.ExistingConnections != "may_survive_with_old_settings" {
				t.Fatal(p)
			}
			progress, code, requires := replaceInbound(t, c, transport, p.Token)
			if code != "" || requires || progress == nil || !progress.Remove.Acknowledged || !progress.Add.Acknowledged || progress.NextAction != "none" {
				t.Fatal(progress, code, requires)
			}
			transferOld("after-successful-replace")
			assertNoListener(t, oldAddress)
			transferNext, nextClosed := openTunnel(t, nextAddress, target)
			transferNext("new-listener-traffic")
			blocked := reserveTCP(t)
			badCandidate := handlerBytes(t, uint32(blocked.Addr().(*net.TCPAddr).Port))
			bad := planInbound(t, c, transport, badCandidate, false)
			progress, code, requires = replaceInbound(t, c, transport, bad.Token)
			if code != "unavailable" || !requires || progress == nil || !progress.Remove.Acknowledged || !progress.Add.Attempted || progress.Add.Acknowledged || progress.NextAction != "replace_configuration" {
				t.Fatal(progress, code, requires)
			}
			token, fenced, generation := replacementStatus(t, c, transport)
			if token != bad.Token || !fenced || generation != 1 {
				t.Fatal("partial replacement status", token, fenced, generation)
			}
			transferOld("old-tunnel-after-failure")
			transferNext("new-tunnel-after-failure")
			assertNoListener(t, nextAddress)
			// The existing durable configuration owner performs the explicit restart.
			// It receives original complete sources, never reconstructed observed users.
			fallbackConfig(t, c, transport, source)
			awaitNative(t, f.native)
			oldClosed()
			nextClosed()
			restored, _ := openTunnel(t, oldAddress, target)
			restored("restored-config-traffic")
			_, tags := nativeRecords(t, c, transport, true)
			found := false
			for _, tag := range tags {
				found = found || tag == "tunnel"
			}
			if !found {
				t.Fatal("fallback did not restore membership")
			}

			lostListener := reserveTCP(t)
			lostAddress := lostListener.Addr().String()
			lostCandidate := handlerBytes(t, uint32(lostListener.Addr().(*net.TCPAddr).Port))
			lostListener.Close()
			lostPlan := planInbound(t, c, transport, lostCandidate, false)
			relay.lose.Store(true)
			progress, code, requires = replaceInbound(t, c, transport, lostPlan.Token)
			relay.lose.Store(false)
			if code != "unavailable" || !requires || progress == nil || !progress.Remove.Acknowledged || progress.Add.Effect != "unknown" || progress.Add.Acknowledged {
				t.Fatal("lost native acknowledgement", progress, code, requires)
			}
			token, fenced, generation = replacementStatus(t, c, transport)
			if token != lostPlan.Token || !fenced || generation != 2 {
				t.Fatal("lost-ack status", token, fenced, generation)
			}
			liveDespiteFailure, _ := openTunnel(t, lostAddress, target)
			liveDespiteFailure("applied-despite-lost-ack")
			restored("previous-tunnel-after-lost-ack")
			// Restoring the reply link does not clear the generation fence.
			_, code, requires = replaceInbound(t, c, transport, lostPlan.Token)
			if code != "restart_required" || !requires {
				t.Fatal("ambiguous replacement replay admitted", code, requires)
			}
			applyFallbackConfig(t, c, transport)
			awaitNative(t, f.native)
			token, fenced, generation = replacementStatus(t, c, transport)
			if token != lostPlan.Token || fenced || generation != 3 {
				t.Fatal("recovery status", token, fenced, generation)
			}
			final, _ := openTunnel(t, oldAddress, target)
			final("explicit-apply-recovered")
			original, err := os.ReadFile(f.config.Instances[0].Xray.Configuration)
			if err != nil || string(original) != string(source) {
				t.Fatal("baseline source changed", err)
			}
		})
	}
}
