//go:build integration

package api_test

import (
	"context"
	"errors"
	"fmt"
	"math"
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

// Official manager/service executes reads/swaps; instrumentation forbids scans
// and can discard an actual acknowledgement. This is not a full engine process.
type pairStatsServer struct {
	native.StatsServiceServer
	reads       atomic.Int32
	resets      atomic.Int32
	loseReplyAt int32
}

func (s *pairStatsServer) QueryStats(context.Context, *native.QueryStatsRequest) (*native.QueryStatsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "pair operations must not scan")
}

func (s *pairStatsServer) GetStats(ctx context.Context, request *native.GetStatsRequest) (*native.GetStatsResponse, error) {
	var call int32
	if request.Reset_ {
		call = s.resets.Add(1)
	} else {
		s.reads.Add(1)
	}
	response, err := s.StatsServiceServer.GetStats(ctx, request)
	if err == nil && request.Reset_ && call == s.loseReplyAt {
		return nil, status.Error(codes.Unavailable, "lost acknowledgement after native swap")
	}
	return response, err
}

func TestPairNativeCallCountIsIndependentOfUnrelatedCounters(t *testing.T) {
	for _, scope := range []domain.TrafficScope{domain.UserTraffic, domain.InboundTraffic, domain.OutboundTraffic} {
		for _, size := range []int{0, 100, 1000} {
			t.Run(fmt.Sprintf("%s/%d", scope, size), func(t *testing.T) {
				manager, err := nativestats.NewManager(t.Context(), &nativestats.Config{})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = manager.Close() })
				selection := domain.TrafficPairSelection{Scope: scope, Name: " exact>>>opaque "}
				for i, direction := range []domain.TrafficDirection{domain.Uplink, domain.Downlink} {
					counter, err := manager.RegisterCounter(string(scope) + ">>>" + selection.Name + ">>>traffic>>>" + string(direction))
					if err != nil {
						t.Fatal(err)
					}
					counter.Set([2]int64{math.MinInt64, math.MaxInt64}[i])
				}
				for i := range size {
					counter, err := manager.RegisterCounter(fmt.Sprintf("other>>>%s>>>%s>>>%d>>>traffic>>>uplink", scope, selection.Name, i))
					if err != nil {
						t.Fatal(err)
					}
					counter.Set(77)
				}
				server := &pairStatsServer{StatsServiceServer: native.NewStatsServer(manager)}
				address, connections := apiServer(t, server)
				client := apiClient(t, address, time.Second, 1024)
				reader := services.NewTrafficPairService(map[instances.InstanceID]services.TrafficPairReadBinding{"edge": {Reader: client, Timeout: time.Second}})
				resetter := services.NewTrafficPairResetService(map[instances.InstanceID]services.TrafficPairResetBinding{"edge": {Resetter: client, Timeout: time.Second}})
				read, err := reader.GetTrafficPair(t.Context(), "edge", selection)
				if err != nil || read.Pair.Uplink == nil || read.Pair.Downlink == nil || read.Pair.Uplink.Bytes != math.MinInt64 || read.Pair.Downlink.Bytes != math.MaxInt64 || server.reads.Load() != 2 || server.resets.Load() != 0 {
					t.Fatalf("pair read: %+v %v", read, err)
				}
				reset, err := resetter.ResetTrafficPair(t.Context(), "edge", selection)
				if err != nil || server.reads.Load() != 2 || server.resets.Load() != 2 || connections.opened.Load() != 1 {
					t.Fatalf("pair reset: %+v %v calls=%d+%d channels=%d", reset, err, server.reads.Load(), server.resets.Load(), connections.opened.Load())
				}
				for i, item := range reset.Items {
					if item.State != domain.TrafficResetAcknowledged || item.Reset == nil || item.Reset.PreviousBytes != [2]int64{math.MinInt64, math.MaxInt64}[i] || manager.GetCounter(string(scope)+">>>"+selection.Name+">>>traffic>>>"+string(item.Direction)).Value() != 0 {
						t.Fatalf("native effect: %+v", item)
					}
				}
				for i := range size {
					if manager.GetCounter(fmt.Sprintf("other>>>%s>>>%s>>>%d>>>traffic>>>uplink", scope, selection.Name, i)).Value() != 77 {
						t.Fatal("pair crossed counter identity")
					}
				}
			})
		}
	}
}

func TestPairNativeMissingDirectionsAreNotSynthesizedOrRegistered(t *testing.T) {
	for _, presence := range []int{0, 1, 2, 3} {
		t.Run(fmt.Sprint(presence), func(t *testing.T) {
			manager, err := nativestats.NewManager(t.Context(), &nativestats.Config{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = manager.Close() })
			for i, direction := range []string{"uplink", "downlink"} {
				if presence&(1<<i) != 0 {
					counter, err := manager.RegisterCounter("user>>>user>>>traffic>>>" + direction)
					if err != nil {
						t.Fatal(err)
					}
					counter.Set(0)
				}
			}
			server := &pairStatsServer{StatsServiceServer: native.NewStatsServer(manager)}
			address, _ := apiServer(t, server)
			client := apiClient(t, address, time.Second, 1024)
			reader := services.NewTrafficPairService(map[instances.InstanceID]services.TrafficPairReadBinding{"edge": {Reader: client, Timeout: time.Second}})
			resetter := services.NewTrafficPairResetService(map[instances.InstanceID]services.TrafficPairResetBinding{"edge": {Resetter: client, Timeout: time.Second}})
			selection := domain.TrafficPairSelection{Scope: domain.UserTraffic, Name: "user"}
			read, err := reader.GetTrafficPair(t.Context(), "edge", selection)
			if err != nil || (read.Pair.Uplink != nil) != (presence&1 != 0) || (read.Pair.Downlink != nil) != (presence&2 != 0) {
				t.Fatalf("native absence: %+v %v", read, err)
			}
			reset, err := resetter.ResetTrafficPair(t.Context(), "edge", selection)
			if err != nil || server.reads.Load() != 2 || server.resets.Load() != 2 {
				t.Fatalf("missing reset: %+v %v", reset, err)
			}
			for i, item := range reset.Items {
				exists := presence&(1<<i) != 0
				if !exists && (item.State != domain.TrafficResetMissing || item.Reset != nil || manager.GetCounter("user>>>user>>>traffic>>>"+string(item.Direction)) != nil) || exists && (item.State != domain.TrafficResetAcknowledged || item.Reset == nil || item.Reset.PreviousBytes != 0) {
					t.Fatalf("missing counter fabricated: %+v", item)
				}
			}
		})
	}
}

func TestPairNativeLostAcknowledgementStopsWithoutReplayingEffects(t *testing.T) {
	for _, lost := range []int32{1, 2} {
		t.Run(fmt.Sprint(lost), func(t *testing.T) {
			manager, err := nativestats.NewManager(t.Context(), &nativestats.Config{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = manager.Close() })
			for _, direction := range []string{"uplink", "downlink"} {
				counter, err := manager.RegisterCounter("user>>>user>>>traffic>>>" + direction)
				if err != nil {
					t.Fatal(err)
				}
				counter.Set(123)
			}
			server := &pairStatsServer{StatsServiceServer: native.NewStatsServer(manager), loseReplyAt: lost}
			address, _ := apiServer(t, server)
			client := apiClient(t, address, time.Second, 1024)
			service := services.NewTrafficPairResetService(map[instances.InstanceID]services.TrafficPairResetBinding{"edge": {Resetter: client, Timeout: time.Second}})
			report, err := service.ResetTrafficPair(t.Context(), "edge", domain.TrafficPairSelection{Scope: domain.UserTraffic, Name: "user"})
			if !errors.Is(err, domain.ErrTrafficResetUnknown) || server.resets.Load() != lost || server.reads.Load() != 0 {
				t.Fatalf("lost reply ignored/retried: %+v %v", report, err)
			}
			for i, item := range report.Items {
				want := int64(123)
				state := domain.TrafficResetNotAttempted
				if int32(i) < lost {
					want = 0
					state = domain.TrafficResetAcknowledged
				}
				if int32(i+1) == lost {
					state = domain.TrafficResetUnknown
				}
				if manager.GetCounter("user>>>user>>>traffic>>>"+string(item.Direction)).Value() != want || item.State != state || (item.Reset != nil) != (state == domain.TrafficResetAcknowledged) {
					t.Fatalf("report differs from effects: %+v", item)
				}
			}
		})
	}
}
