//go:build integration && linux

package integration_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	"github.com/PapilioVPN/node/internal/testutil"
	stats "github.com/xtls/xray-core/app/stats/command"
)

func TestRealXrayExactCountersAcrossIndependentTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f := newFixture(t, pki, strings.Replace(broker.ClientURL(), "nats://", "tls://", 1), transport)
			selections := []domain.Selection{}
			for _, scope := range []domain.Scope{domain.User, domain.Inbound, domain.Outbound} {
				name := map[domain.Scope]string{domain.User: account, domain.Inbound: inboundTag, domain.Outbound: outboundTag}[scope]
				for _, direction := range []domain.Direction{domain.Uplink, domain.Downlink} {
					selection := domain.Selection{Scope: scope, Name: name, Direction: direction}
					selections = append(selections, selection)
					before := f.read(t, transport, selection)
					if scope == domain.User {
						if before.Error != "counter_not_found" {
							t.Fatalf("unregistered user counter became zero %+v", before)
						}
					} else if before.Error != "" || before.Bytes != 0 {
						t.Fatalf("registered zero lost %+v", before)
					}
				}
			}
			f.sendTraffic(t)
			for _, selection := range selections {
				first := f.read(t, transport, selection)
				second := f.read(t, transport, selection)
				if first.Error != "" || first.Bytes <= 0 || first.Bytes != second.Bytes || first.Generation != 1 || first.Source != domain.Source || first.InstanceID != "edge-a" || first.Name != selection.Name || first.Scope != selection.Scope || first.Direction != selection.Direction || first.ObservedAt.IsZero() || first.DurationNS < 0 {
					t.Fatalf("invalid observation first=%+v second=%+v", first, second)
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				native, err := f.api.GetStats(ctx, &stats.GetStatsRequest{Name: string(selection.Scope) + ">>>" + selection.Name + ">>>traffic>>>" + string(selection.Direction)})
				cancel()
				if err != nil || native.Stat.Value != first.Bytes {
					t.Fatal("Node changed native bytes", native, err)
				}
			}
			for _, scope := range []domain.Scope{domain.User, domain.Inbound, domain.Outbound} {
				missing := f.read(t, transport, domain.Selection{Scope: scope, Name: "absent", Direction: domain.Downlink})
				if missing.Error != "counter_not_found" {
					t.Fatalf("missing counter returned success %+v", missing)
				}
			}
			data, err := os.ReadFile(f.config.Instances[0].Xray.Configuration)
			if err != nil || string(data) != string(f.original) {
				t.Fatal("read changed native configuration", err)
			}
		})
	}
}
