//go:build integration && linux

package integration_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/domain"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestRealXrayOrdinaryBurstAndRestartAcrossTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	address := strings.Replace(broker.ClientURL(), "nats://", "tls://", 1)
	for _, mode := range []string{"ordinary", "burst"} {
		for _, transport := range []string{"http", "grpc", "nats"} {
			t.Run(mode+"/"+transport, func(t *testing.T) {
				f := newFixture(t, pki, address, transport, mode, 0)
				good := f.awaitStatus(t, "probe-good")
				bad := f.awaitStatus(t, "probe-bad")
				if !good.Status.Alive || good.Status.LatencyMS == nil || good.Status.Mode != mode || good.Generation != 1 || good.Source != domain.Source || good.ObservedAt.IsZero() || good.DurationNS < 0 {
					t.Fatalf("bad native success %+v %+v", good, good.Status)
				}
				if bad.Status.Alive || bad.Status.LatencyMS != nil {
					t.Fatalf("failed probe became valid latency %+v", bad.Status)
				}
				if mode == "ordinary" {
					if good.Status.SampledAt == nil || good.Status.LastTryUnixSec <= 0 || good.Status.LastSeenUnixSec <= 0 || good.Status.Freshness != "fresh" || good.Status.HealthPing != nil {
						t.Fatalf("ordinary time lost %+v", good.Status)
					}
					until := time.Unix(good.Status.LastTryUnixSec+2, 0)
					if delay := time.Until(until); delay > 0 {
						timer := time.NewTimer(delay)
						<-timer.C
					}
					stale := f.get(t, "edge", "probe-good", 1)
					if stale.Status.Freshness != "stale" || stale.Status.SampleAgeSeconds == nil {
						t.Fatal(stale.Status)
					}
				} else {
					h := good.Status.HealthPing
					if h == nil || h.All <= 0 || h.Fail != 0 || h.AverageNS <= 0 || h.AverageNS/1e6 != good.Status.DelayMS || good.Status.SampledAt != nil || good.Status.LastTryUnixSec != 0 || good.Status.LastSeenUnixSec != 0 || good.Status.Freshness != "unknown" {
						t.Fatalf("burst sample fabricated %+v", good.Status)
					}
				}
				absent := f.get(t, "edge", "not-observed", 0)
				if absent.Error != "" || absent.Status != nil || absent.Generation != 1 {
					t.Fatal(absent)
				}
				for _, tt := range []struct{ id, want string }{{"disabled", "api_not_configured"}, {"unreachable", "unavailable"}, {"absent", "not_found"}} {
					if o := f.get(t, tt.id, "probe-good", 0); o.Error != tt.want {
						t.Fatal(tt, o)
					}
				}
				first := f.list(t, "", 1)
				if first.Error != "" || first.Total != 2 || len(first.Statuses) != 1 || first.Token == "" {
					t.Fatal(first)
				}
				second := f.list(t, first.Token, 50)
				if second.Error != "" || len(second.Statuses) != 1 || second.Token != "" || !first.ObservedAt.Equal(second.ObservedAt) {
					t.Fatal(second)
				}
				if mode == "ordinary" && f.probes.Load() != 1 {
					t.Fatalf("observational Node reads triggered extra probes: %d", f.probes.Load())
				}
				// Hold the next real native success so a post-restart read cannot reuse old
				// observations merely because the new process has already completed its probe.
				f.blocked.Store(true)
				f.control(t, "restart")
				f.waitNative(t)
				if old := f.list(t, first.Token, 1); old.Error != "continuation_expired" {
					t.Fatal(old)
				}
				reset := f.get(t, "edge", "probe-good", 0)
				if reset.Error != "" || reset.Status != nil || reset.Generation != 2 {
					t.Fatal("old sample survived restart", reset)
				}
				f.unblock()
				after := f.awaitStatus(t, "probe-good")
				if after.Generation != 2 || !after.Status.Alive {
					t.Fatal(after)
				}
				original, err := os.ReadFile(f.config.Instances[0].Xray.Configuration)
				if err != nil || string(original) != string(f.original) {
					t.Fatal("observation changed configuration", err)
				}
				f.control(t, "stop")
				if o := f.get(t, "edge", "probe-good", 0); o.Error != "not_running" || o.Status != nil {
					t.Fatal(o)
				}
			})
		}
	}
}
func TestRealEmptyUnsupportedAndFiftyOneRecords(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	address := strings.Replace(broker.ClientURL(), "nats://", "tls://", 1)
	for _, transport := range []string{"http", "grpc", "nats"} {
		for _, mode := range []string{"empty", "unsupported", "ordinary"} {
			t.Run(transport+"/"+mode, func(t *testing.T) {
				extra := 0
				if mode == "ordinary" {
					extra = 49
				}
				f := newFixture(t, pki, address, transport, mode, extra)
				if mode == "unsupported" {
					if got := f.get(t, "edge", "probe-good", 0); got.Error != "unsupported" {
						t.Fatal(got)
					}
					return
				}
				if mode == "empty" {
					got := f.get(t, "edge", "probe-good", 0)
					p := f.list(t, "", 0)
					if got.Error != "" || got.Status != nil || p.Error != "" || len(p.Statuses) != 0 || p.Statuses == nil {
						t.Fatal(got, p)
					}
					return
				}
				deadline := time.Now().Add(4 * time.Second)
				var p page
				for {
					p = f.list(t, "", 0)
					if p.Total == 51 {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal(p)
					}
					time.Sleep(20 * time.Millisecond)
				}
				if len(p.Statuses) != 50 || p.Token == "" || p.Error != "" {
					t.Fatal(p)
				}
				next := f.list(t, p.Token, 0)
				if next.Error != "" || len(next.Statuses) != 1 || next.Token != "" || !next.ObservedAt.Equal(p.ObservedAt) {
					t.Fatal(next)
				}
				if invalid := f.list(t, "", 51); invalid.Error != "invalid_argument" {
					t.Fatal(invalid)
				}
			})
		}
	}
}
