//go:build integration

package api_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	nativestats "github.com/xtls/xray-core/app/stats"
	native "github.com/xtls/xray-core/app/stats/command"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/traffic/app/services"
	"github.com/PapilioVPN/node/internal/modules/xray/traffic/domain"
	trafficapi "github.com/PapilioVPN/node/internal/modules/xray/traffic/infra/api"
	nativeapi "github.com/PapilioVPN/node/internal/platform/xray"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

type delayedResetServer struct {
	*scopeResetServer
	delay time.Duration
}

func (s delayedResetServer) GetStats(ctx context.Context, request *native.GetStatsRequest) (*native.GetStatsResponse, error) {
	if s.delay > 0 {
		timer := time.NewTimer(s.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.scopeResetServer.GetStats(ctx, request)
}

// This benchmarks the actual SDK manager/RPC path on loopback, with either no
// injected latency or 1ms per swap. It is not a standalone Xray/PasarGuard comparison.
func BenchmarkScopeResetNativeRPC(b *testing.B) {
	for _, delay := range []time.Duration{0, time.Millisecond} {
		for _, size := range []int{0, 1, 100, 1000} {
			for _, concurrency := range []int{1, 4} {
				b.Run(fmt.Sprintf("delay=%s/N=%d/C=%d", delay, size, concurrency), func(b *testing.B) {
					manager, err := nativestats.NewManager(b.Context(), &nativestats.Config{})
					if err != nil {
						b.Fatal(err)
					}
					b.Cleanup(func() { _ = manager.Close() })
					for i := range size {
						if _, err := manager.RegisterCounter(fmt.Sprintf("user>>>counter-%04d>>>traffic>>>uplink", i)); err != nil {
							b.Fatal(err)
						}
					}
					server := &scopeResetServer{StatsServiceServer: native.NewStatsServer(manager)}
					address, connections := testutil.XrayStatsAPI(b, delayedResetServer{scopeResetServer: server, delay: delay})
					resource, err := nativeapi.NewClient(nativeapi.Config{Network: "tcp", Address: address, RequestTimeout: 30 * time.Second, MaxReceiveBytes: 256 * 1024, MaxConcurrent: concurrency})
					if err != nil {
						b.Fatal(err)
					}
					b.Cleanup(func() {
						if err := resource.Close(); err != nil {
							b.Error(err)
						}
					})
					adapter := &trafficapi.Counters{API: resource}
					service := services.NewTrafficCountersResetService(map[instances.InstanceID]services.TrafficResetBinding{"edge": {Reader: adapter, Resetter: adapter, Timeout: 30 * time.Second, MaxConcurrent: concurrency}})
					if _, err := adapter.ReadTrafficCounters(b.Context(), domain.UserTraffic); err != nil {
						b.Fatal(err)
					}
					b.ReportAllocs()
					for b.Loop() {
						result, err := service.ResetTrafficCounters(b.Context(), "edge", domain.UserTraffic, max(size, 1))
						if err != nil || len(result.Reset.Items) != size {
							b.Fatalf("scope reset: %v", err)
						}
					}
					if server.queries.Load() != int32(b.N+1) || server.resets.Load() != int32(b.N*size) || connections.Opened.Load() != 1 {
						b.Fatalf("call/channel growth: reads=%d resets=%d channels=%d", server.queries.Load(), server.resets.Load(), connections.Opened.Load())
					}
					b.ReportMetric(float64(size+1), "native_calls/op")
				})
			}
		}
	}
}
