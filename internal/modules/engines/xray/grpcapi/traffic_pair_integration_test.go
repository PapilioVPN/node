//go:build integration

package grpcapi_test

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	nativestats "github.com/xtls/xray-core/app/stats"
	native "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/app/services"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/infra/api"
	"github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

// Official SDK counters perform the actual effects. The interceptor injects a
// lost native acknowledgement; bootstrap covers real engine processes separately.
func TestMTLSPairResetPreservesUnknownEffectsAndIndependentMissingDirections(t *testing.T) {
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
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var swaps atomic.Int32
	nativeServer := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		response, err := handler(ctx, request)
		if info.FullMethod == native.StatsService_GetStats_FullMethodName && request.(*native.GetStatsRequest).Reset_ && swaps.Add(1) == 2 && err == nil {
			return nil, status.Error(codes.Unavailable, "private lost acknowledgement after swap")
		}
		return response, err
	}))
	native.RegisterStatsServiceServer(nativeServer, native.NewStatsServer(manager))
	done := make(chan struct{})
	go func() { defer close(done); _ = nativeServer.Serve(listener) }()
	t.Cleanup(func() { nativeServer.Stop(); nativeServer.GracefulStop(); <-done })
	backend, err := api.NewClient(api.Config{Network: "tcp", Address: listener.Addr().String(), RequestTimeout: time.Second, MaxReceiveBytes: 4096, MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	reads := services.NewTrafficPairService(map[instances.InstanceID]services.TrafficPairReadBinding{"edge": {Reader: backend, Timeout: time.Second}})
	resets := services.NewTrafficPairResetService(map[instances.InstanceID]services.TrafficPairResetBinding{"edge": {Resetter: backend, Timeout: time.Second}})
	certificates := testutil.NewCertificates(t)
	management, err := grpcserver.NewServer(certificates.Config().Communications.GRPC, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = management.Close() })
	xrayv1.RegisterXrayServiceServer(management.Registrar(), grpcapi.Handler{AllowStatsRead: true, AllowStatsReset: true, Pairs: reads, PairResets: resets})
	if err := management.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	connection, err := grpc.NewClient(management.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(certificates.ClientTLS(t))))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	client := xrayv1.NewXrayServiceClient(connection)
	query := &xrayv1.GetTrafficPairRequest{InstanceId: "edge", Scope: xrayv1.TrafficScope_TRAFFIC_SCOPE_USER, Name: "user"}
	read, err := client.GetTrafficPair(ctx, query)
	if err != nil || read.Uplink == nil || read.Downlink == nil || read.Uplink.Bytes != 123 || read.Downlink.Bytes != 123 || swaps.Load() != 0 {
		t.Fatalf("read changed native usage: %v %v", read, err)
	}
	request := &xrayv1.ResetTrafficPairRequest{InstanceId: query.InstanceId, Scope: query.Scope, Name: query.Name}
	response, err := client.ResetTrafficPair(ctx, request)
	if response != nil || status.Code(err) != codes.Unavailable || swaps.Load() != 2 {
		t.Fatalf("lost pair reply changed/replayed: %v %v", response, err)
	}
	details := status.Convert(err).Details()
	if len(details) != 1 {
		t.Fatalf("pair progress absent over mTLS: %v", err)
	}
	failure, ok := details[0].(*xrayv1.TrafficPairResetFailure)
	if !ok || failure.Progress == nil || len(failure.Progress.Items) != 2 {
		t.Fatalf("pair progress: %v", details)
	}
	items := failure.Progress.Items
	if items[0].State != xrayv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_ACKNOWLEDGED || items[0].Acknowledgement == nil || items[0].Acknowledgement.PreviousBytes != 123 || items[1].State != xrayv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_UNKNOWN || items[1].Acknowledgement != nil {
		t.Fatalf("pair report erased effects: %v", failure)
	}
	for _, direction := range []string{"uplink", "downlink"} {
		if manager.GetCounter("user>>>user>>>traffic>>>"+direction).Value() != 0 {
			t.Fatal("native swap was not applied before lost reply")
		}
	}
	for missing := range 2 {
		absent, present := "downlink", "uplink"
		if missing == 1 {
			absent, present = present, absent
		}
		if err := manager.UnregisterCounter("user>>>user>>>traffic>>>" + absent); err != nil {
			t.Fatal(err)
		}
		counter, err := manager.GetOrRegisterCounter("user>>>user>>>traffic>>>" + present)
		if err != nil {
			t.Fatal(err)
		}
		counter.Set(7)
		read, err := client.GetTrafficPair(ctx, query)
		if err != nil || (read.Uplink == nil) != (absent == "uplink") || (read.Downlink == nil) != (absent == "downlink") {
			t.Fatalf("wire missing direction: %v %v", read, err)
		}
		response, err := client.ResetTrafficPair(ctx, request)
		if err != nil || len(response.Items) != 2 {
			t.Fatalf("pair missing reset: %v %v", response, err)
		}
		for i, direction := range []string{"uplink", "downlink"} {
			item := response.Items[i]
			if direction == absent {
				if item.State != xrayv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_MISSING || item.Acknowledgement != nil || manager.GetCounter("user>>>user>>>traffic>>>"+absent) != nil {
					t.Fatalf("missing counter fabricated: %v", item)
				}
			} else if item.State != xrayv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_ACKNOWLEDGED || item.Acknowledgement == nil || item.Acknowledgement.PreviousBytes != 7 || counter.Value() != 0 {
				t.Fatalf("present direction was skipped: %v", item)
			}
		}
	}
	if swaps.Load() != 6 {
		t.Fatalf("pair reset was replayed or skipped a direction: %d", swaps.Load())
	}
}
