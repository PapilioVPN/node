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
	"github.com/PapilioVPN/node/internal/modules/xray/traffic/app/services"
	"github.com/PapilioVPN/node/internal/modules/xray/traffic/domain"
	trafficapi "github.com/PapilioVPN/node/internal/modules/xray/traffic/infra/api"
	nativeapi "github.com/PapilioVPN/node/internal/platform/xray"
	"github.com/PapilioVPN/node/internal/shared/testutil"
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
		for _, test := range []struct{ size, concurrency int }{{0, 1}, {0, 4}, {1, 1}, {1, 4}, {100, 1}, {100, 4}, {1000, 1}, {1000, 4}} {
			size, concurrency := test.size, test.concurrency
			t.Run(fmt.Sprintf("%s/%d/C=%d", scope, size, concurrency), func(t *testing.T) {
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
				address, connections := testutil.XrayStatsAPI(t, server)
				resource, err := nativeapi.NewClient(nativeapi.Config{Network: "tcp", Address: address, RequestTimeout: 5 * time.Second, MaxReceiveBytes: 256 * 1024, MaxConcurrent: concurrency})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := resource.Close(); err != nil {
						t.Error(err)
					}
				})
				client := &trafficapi.Counters{API: resource}
				service := services.NewTrafficCountersResetService(map[instances.InstanceID]services.TrafficResetBinding{"edge": {Reader: client, Resetter: client, Timeout: 5 * time.Second, MaxConcurrent: concurrency}})
				result, err := service.ResetTrafficCounters(t.Context(), "edge", scope, max(size, 1))
				if err != nil || len(result.Reset.Items) != size || server.queries.Load() != 1 || server.resets.Load() != int32(size) || connections.Opened.Load() != 1 {
					t.Fatalf("scope reset: %+v %v calls=%d+%d connections=%d", result, err, server.queries.Load(), server.resets.Load(), connections.Opened.Load())
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
	address, _ := testutil.XrayStatsAPI(t, server)
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

func TestNativeBulkResetFacilitiesCannotReplaceExactScopeSelection(t *testing.T) {
	manager, err := nativestats.NewManager(t.Context(), &nativestats.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	selected, err := manager.RegisterCounter("user>>>selected>>>traffic>>>uplink")
	if err != nil {
		t.Fatal(err)
	}
	collision, err := manager.RegisterCounter("outbound>>>user>>>collision>>>traffic>>>uplink")
	if err != nil {
		t.Fatal(err)
	}
	selected.Set(17)
	collision.Set(29)
	server := native.NewStatsServer(manager)
	response, err := server.QueryStats(t.Context(), &native.QueryStatsRequest{Pattern: "user>>>", Reset_: true})
	if err != nil || len(response.GetStat()) != 2 || selected.Value() != 0 || collision.Value() != 0 {
		t.Fatalf("native substring reset semantics changed: %v %v", response, err)
	}
	selected.Set(17)
	users, err := server.GetUsersStats(t.Context(), &native.GetUsersStatsRequest{IncludeTraffic: true, Reset_: true})
	if err != nil || len(users.GetUsers()) != 0 || selected.Value() != 17 {
		t.Fatalf("native online-only reset semantics changed: %v %v", users, err)
	}
}

func TestConcurrentScopeResetRetainsActualNativeEffectsAfterLostAcknowledgement(t *testing.T) {
	const size, concurrency = 10, 3
	manager, err := nativestats.NewManager(t.Context(), &nativestats.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	for i := range size {
		counter, err := manager.RegisterCounter(fmt.Sprintf("user>>>counter-%04d>>>traffic>>>uplink", i))
		if err != nil {
			t.Fatal(err)
		}
		counter.Set(123)
	}
	server := &scopeResetServer{StatsServiceServer: native.NewStatsServer(manager), loseReplyAt: 2}
	address, connections := testutil.XrayStatsAPI(t, server)
	resource, err := nativeapi.NewClient(nativeapi.Config{Network: "tcp", Address: address, RequestTimeout: time.Second, MaxReceiveBytes: 4096, MaxConcurrent: concurrency})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := resource.Close(); err != nil {
			t.Error(err)
		}
	})
	adapter := &trafficapi.Counters{API: resource}
	service := services.NewTrafficCountersResetService(map[instances.InstanceID]services.TrafficResetBinding{"edge": {Reader: adapter, Resetter: adapter, Timeout: time.Second, MaxConcurrent: concurrency}})
	report, err := service.ResetTrafficCounters(t.Context(), "edge", domain.UserTraffic, size)
	if !errors.Is(err, domain.ErrTrafficResetUnknown) || len(report.Reset.Items) != size || server.queries.Load() != 1 || connections.Opened.Load() != 1 {
		t.Fatalf("lost native failure or resource reuse: %v", err)
	}
	var acknowledged, unknown int32
	for i, item := range report.Reset.Items {
		if item.Name != fmt.Sprintf("counter-%04d", i) {
			t.Fatal("completion reordered identities")
		}
		counter := manager.GetCounter("user>>>" + item.Name + ">>>traffic>>>uplink")
		switch item.State {
		case domain.TrafficResetAcknowledged:
			acknowledged++
			if item.Reset == nil || item.Reset.PreviousBytes != 123 || counter.Value() != 0 {
				t.Fatalf("acknowledged native effect lost: %+v", item)
			}
		case domain.TrafficResetUnknown:
			unknown++
			if item.Reset != nil || counter.Value() != 0 {
				t.Fatalf("lost acknowledgement fabricated no effect: %+v", item)
			}
		case domain.TrafficResetNotAttempted:
			if item.Reset != nil || counter.Value() != 123 {
				t.Fatalf("unattempted entry was reset: %+v", item)
			}
		default:
			t.Fatalf("unexpected native effect: %+v", item)
		}
	}
	if unknown != 1 || acknowledged+unknown != server.resets.Load() {
		t.Fatalf("lost, repeated or unjoined effects: ack=%d unknown=%d calls=%d", acknowledged, unknown, server.resets.Load())
	}
}

// Controlled native calls expose cancellation and resource-close behavior after
// dispatch. No standalone Xray effect is claimed for this fixture.
type blockedScopeResetServer struct {
	native.UnimplementedStatsServiceServer
	started chan struct{}
}

func (s blockedScopeResetServer) QueryStats(context.Context, *native.QueryStatsRequest) (*native.QueryStatsResponse, error) {
	response := &native.QueryStatsResponse{}
	for i := range 8 {
		response.Stat = append(response.Stat, &native.Stat{Name: fmt.Sprintf("user>>>counter-%04d>>>traffic>>>uplink", i)})
	}
	return response, nil
}

func (s blockedScopeResetServer) GetStats(ctx context.Context, _ *native.GetStatsRequest) (*native.GetStatsResponse, error) {
	s.started <- struct{}{}
	<-ctx.Done()
	return nil, status.FromContextError(ctx.Err()).Err()
}

func TestConcurrentScopeResetJoinsCallsOnCancellationDeadlineAndResourceClose(t *testing.T) {
	const limit = 3
	for _, mode := range []string{"cancel", "deadline", "close"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{}, 8)
			address, _ := testutil.XrayStatsAPI(t, blockedScopeResetServer{started: started})
			resource, err := nativeapi.NewClient(nativeapi.Config{Network: "tcp", Address: address, RequestTimeout: 2 * time.Second, MaxReceiveBytes: 4096, MaxConcurrent: limit})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = resource.Close() })
			adapter := &trafficapi.Counters{API: resource}
			service := services.NewTrafficCountersResetService(map[instances.InstanceID]services.TrafficResetBinding{"edge": {Reader: adapter, Resetter: adapter, Timeout: 2 * time.Second, MaxConcurrent: limit}})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			type outcome struct {
				report domain.InstanceTrafficCountersReset
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				r, e := service.ResetTrafficCounters(ctx, "edge", domain.UserTraffic, 8)
				done <- outcome{r, e}
			}()
			for i := 0; i < limit; i++ {
				select {
				case <-started:
				case <-time.After(time.Second):
					cancel()
					<-done
					t.Fatal("native resets did not overlap")
				}
			}
			switch mode {
			case "cancel":
				cancel()
			case "close":
				if err := resource.Close(); err != nil {
					t.Fatal(err)
				}
			}
			result := <-done
			if !errors.Is(result.err, domain.ErrTrafficResetUnknown) || len(result.report.Reset.Items) != 8 {
				t.Fatalf("lost in-flight uncertainty or join: %v", result.err)
			}
			if mode == "cancel" && !errors.Is(result.err, context.Canceled) || mode == "deadline" && !errors.Is(result.err, context.DeadlineExceeded) {
				t.Fatalf("lost termination cause: %v", result.err)
			}
			for i, item := range result.report.Reset.Items {
				want := domain.TrafficResetNotAttempted
				if i < limit {
					want = domain.TrafficResetUnknown
				}
				if item.State != want || item.Reset != nil {
					t.Fatalf("termination invented or discarded effect: %+v", item)
				}
			}
		})
	}
}
