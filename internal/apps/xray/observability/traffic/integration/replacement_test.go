//go:build integration && linux

package integration_test

import (
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/testutil"
)

func TestReplacementRejectsContinuationAndRecoversUnknownReset(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f := newFixture(t, pki, strings.Replace(broker.ClientURL(), "nats://", "tls://", 1), transport, fixtureOptions{extraOutbounds: 26, relay: true})
			f.sendTraffic(t)
			page := invokeDecode[trafficPage](t, f, transport, "list", map[string]any{"scope": "outbound"})
			initial := invokeDecode[resetResult](t, f, transport, "reset_status", nil)
			f.relay.loseAt.Store(1)
			input := resetInput(initial, "user", account, "lost-before-replacement")
			input["direction"] = "uplink"
			unknown := invokeDecode[resetResult](t, f, transport, "reset_counter", input)
			if unknown.Outcome != "unknown" || !unknown.RecoveryRequired {
				t.Fatal(unknown)
			}
			f.restart(t, transport)
			continued := invokeDecode[trafficPage](t, f, transport, "list", map[string]any{"scope": "outbound", "page_token": page.NextPageToken})
			if continued.Error != "continuation_expired" {
				t.Fatal("old observation crossed generation", continued)
			}
			retained := invokeDecode[resetResult](t, f, transport, "reset_status", nil)
			if retained.OperationID != unknown.OperationID || retained.Generation != 1 || retained.Outcome != "unknown" {
				t.Fatal("replacement overwrote history", retained)
			}
			f.sendTraffic(t)
			input = resetInput(retained, "user", account, "reset-after-replacement")
			input["direction"] = "uplink"
			report := invokeDecode[resetResult](t, f, transport, "reset_counter", input)
			if report.Outcome != "completed" || report.Generation != 2 || report.RecoveryRequired || f.relay.resets.Load() != 2 {
				t.Fatal(report)
			}
		})
	}
}
