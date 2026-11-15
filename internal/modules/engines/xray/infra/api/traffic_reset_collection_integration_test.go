//go:build integration

package api_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	nativestats "github.com/xtls/xray-core/app/stats"
	native "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/app/services"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

// The official manager/service executes real atomic swaps; controlled wrappers
// count calls and model a lost acknowledgement. This is not a full Xray process.
type scopeResetServer struct {
	native.StatsServiceServer
	queries     atomic.Int32
	resets      atomic.Int32
	loseReplyAt int32
}

func (s *scopeResetServer) QueryStats(ctx context.Context, request *native.QueryStatsRequest) (*native.QueryStatsResponse, error) {
	s.queries.Add(1)
	if request.Reset_ {
		return nil, status.Error(codes.InvalidArgument, "discovery must not reset")
	}
	return s.StatsServiceServer.QueryStats(ctx, request)
}

func (s *scopeResetServer) GetStats(ctx context.Context, request *native.GetStatsRequest) (*native.GetStatsResponse, error) {
	if !request.Reset_ {
		return nil, status.Error(codes.InvalidArgument, "expected reset")
	}
	call := s.resets.Add(1)
	response, err := s.StatsServiceServer.GetStats(ctx, request)
	if err == nil && call == s.loseReplyAt {
		return nil, status.Error(codes.Unavailable, "acknowledgement lost after swap")
	}
	return response, err
}

func TestScopeResetUsesOneDiscoveryAndNExactNativeSwapsWithoutSubstringEffects(t *testing.T) {
	for _, scope := range []domain.TrafficScope{domain.UserTraffic, domain.InboundTraffic, domain.OutboundTraffic} {
		for _, size := range []int{0, 1, 100, 1000} {
			t.Run(fmt.Sprintf("%s/%d", scope, size), func(t *testing.T) {
				manager, err := nativestats.NewManager(t.Context(), &nativestats.Config{})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = manager.Close() })
				prefix := string(scope) + ">>>"
				for i := range size {
					counter, err := manager.RegisterCounter(fmt.Sprintf("%s exact>>>%04d >>>traffic>>>uplink", prefix, i))
					if err != nil {
						t.Fatal(err)
					}
					counter.Set(int64(i + 1))
				}
				sentinels := []string{"other>>>" + prefix + "collision>>>traffic>>>uplink", prefix + "not-traffic>>>online", prefix + "not-traffic>>>traffic>>>unsupported"}
				for _, name := range sentinels {
					counter, err := manager.RegisterCounter(name)
					if err != nil {
						t.Fatal(err)
					}
					counter.Set(77)
				}
				server := &scopeResetServer{StatsServiceServer: native.NewStatsServer(manager)}
				address, connections := apiServer(t, server)
				client := apiClient(t, address, 5*time.Second, 256*1024)
				service := services.NewTrafficCountersResetService(map[instances.InstanceID]services.TrafficResetBinding{"edge": {Reader: client, Resetter: client, Timeout: 5 * time.Second}})
				result, err := service.ResetTrafficCounters(t.Context(), "edge", scope, max(size, 1))
				if err != nil || len(result.Reset.Items) != size || server.queries.Load() != 1 || server.resets.Load() != int32(size) || connections.opened.Load() != 1 {
					t.Fatalf("scope reset: %+v %v calls=%d+%d connections=%d", result, err, server.queries.Load(), server.resets.Load(), connections.opened.Load())
				}
				for i, item := range result.Reset.Items {
					name := fmt.Sprintf(" exact>>>%04d ", i)
					if item.Name != name || item.Direction != domain.Uplink || item.State != domain.TrafficResetAcknowledged || item.Reset == nil || item.Reset.PreviousBytes != int64(i+1) || manager.GetCounter(prefix+name+">>>traffic>>>uplink").Value() != 0 {
						t.Fatalf("native identity/value lost: %+v", item)
					}
				}
				for _, name := range sentinels {
					if manager.GetCounter(name).Value() != 77 {
						t.Fatalf("reset crossed selection: %s", name)
					}
				}
			})
		}
	}
}

func TestScopeResetStopsAfterLostNativeAcknowledgementWithHonestProgress(t *testing.T) {
	manager, err := nativestats.NewManager(t.Context(), &nativestats.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	for _, name := range []string{"a", "b", "c"} {
		counter, err := manager.RegisterCounter("user>>>" + name + ">>>traffic>>>uplink")
		if err != nil {
			t.Fatal(err)
		}
		counter.Set(123)
	}
	server := &scopeResetServer{StatsServiceServer: native.NewStatsServer(manager), loseReplyAt: 2}
	address, _ := apiServer(t, server)
	client := apiClient(t, address, time.Second, 4096)
	service := services.NewTrafficCountersResetService(map[instances.InstanceID]services.TrafficResetBinding{"edge": {Reader: client, Resetter: client, Timeout: time.Second}})
	result, err := service.ResetTrafficCounters(t.Context(), "edge", domain.UserTraffic, 3)
	if !errors.Is(err, domain.ErrTrafficResetUnknown) || server.queries.Load() != 1 || server.resets.Load() != 2 || len(result.Reset.Items) != 3 {
		t.Fatalf("lost reply changed/retried progress: %+v %v", result, err)
	}
	for i, want := range []domain.TrafficResetState{domain.TrafficResetAcknowledged, domain.TrafficResetUnknown, domain.TrafficResetNotAttempted} {
		if result.Reset.Items[i].State != want {
			t.Fatalf("item %d: %+v", i, result.Reset.Items[i])
		}
	}
	if result.Reset.Items[0].Reset == nil || result.Reset.Items[0].Reset.PreviousBytes != 123 || result.Reset.Items[1].Reset != nil || result.Reset.Items[2].Reset != nil {
		t.Fatal("unacknowledged bytes fabricated")
	}
	for _, name := range []string{"a", "b"} {
		if manager.GetCounter("user>>>"+name+">>>traffic>>>uplink").Value() != 0 {
			t.Fatalf("effect was not actually applied: %s", name)
		}
	}
	if manager.GetCounter("user>>>c>>>traffic>>>uplink").Value() != 123 {
		t.Fatal("reset continued after lost reply")
	}
}
