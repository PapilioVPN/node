//go:build integration

package grpcapi_test

import (
	"context"
	"fmt"
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
	"github.com/PapilioVPN/node/internal/modules/xray/grpcapi"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/xray/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/modules/xray/traffic/app/services"
	trafficgrpc "github.com/PapilioVPN/node/internal/modules/xray/traffic/grpcapi"
	trafficv1 "github.com/PapilioVPN/node/internal/modules/xray/traffic/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/modules/xray/traffic/infra/api"
	nativeapi "github.com/PapilioVPN/node/internal/platform/engines/xray/api"
	"github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

// Actual SDK swaps followed by an injected lost reply verify partial progress
// through the real mTLS boundary; real engine processes are tested in bootstrap.
func TestMTLSScopeResetRetainsAppliedUnknownAndUnattemptedEffects(t *testing.T) {
	manager, err := nativestats.NewManager(t.Context(), &nativestats.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	const size = 1000
	for i := range size {
		name := fmt.Sprintf("user-%04d", i)
		counter, err := manager.RegisterCounter("user>>>" + name + ">>>traffic>>>uplink")
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
		if info.FullMethod == native.StatsService_GetStats_FullMethodName && err == nil && swaps.Add(1) == 2 {
			return nil, status.Error(codes.Unavailable, "private native reply lost after swap")
		}
		return response, err
	}))
	native.RegisterStatsServiceServer(nativeServer, native.NewStatsServer(manager))
	nativeDone := make(chan struct{})
	go func() { defer close(nativeDone); _ = nativeServer.Serve(listener) }()
	t.Cleanup(func() { nativeServer.Stop(); nativeServer.GracefulStop(); <-nativeDone })
	resource, err := nativeapi.NewClient(nativeapi.Config{Network: "tcp", Address: listener.Addr().String(), RequestTimeout: time.Second, MaxReceiveBytes: 256 * 1024, MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resource.Close() })
	backend := &api.Counters{API: resource}
	service := services.NewTrafficCountersResetService(map[instances.InstanceID]services.TrafficResetBinding{"edge": {Reader: backend, Resetter: backend, Timeout: time.Second}})
	certificates := testutil.NewCertificates(t)
	management, err := grpcserver.NewServer(certificates.Config().Communications.GRPC, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = management.Close() })
	xrayv1.RegisterXrayServiceServer(management.Registrar(), grpcapi.Handler{TrafficAPI: trafficgrpc.TrafficAPI{AllowStatsReset: true, ScopeResets: service}})
	if err := management.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	connection, err := grpc.NewClient(management.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(certificates.ClientTLS(t))))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	request, release := context.WithTimeout(t.Context(), 3*time.Second)
	defer release()
	result, err := xrayv1.NewXrayServiceClient(connection).ResetTrafficCounters(request, &trafficv1.ResetTrafficCountersRequest{InstanceId: "edge", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_USER, MaxCounters: size})
	if result != nil || status.Code(err) != codes.Unavailable || swaps.Load() != 2 {
		t.Fatalf("native failure changed/retried: %v %v", result, err)
	}
	details := status.Convert(err).Details()
	if len(details) != 1 {
		t.Fatalf("partial report missing: %v", err)
	}
	failure, ok := details[0].(*trafficv1.TrafficCountersResetFailure)
	if !ok || failure.Progress == nil || len(failure.Progress.Items) != size || failure.Progress.SelectedAtUnixMs == nil {
		t.Fatalf("partial wire report: %v", details)
	}
	for i, item := range failure.Progress.Items {
		state := trafficv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_NOT_ATTEMPTED
		if i == 0 {
			state = trafficv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_ACKNOWLEDGED
		}
		if i == 1 {
			state = trafficv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_UNKNOWN
		}
		if item.State != state || item.Name != fmt.Sprintf("user-%04d", i) || (item.Acknowledgement != nil) != (i == 0) {
			t.Fatalf("wire progress changed: %v", item)
		}
		want := int64(123)
		if i < 2 {
			want = 0
		}
		if manager.GetCounter("user>>>"+item.Name+">>>traffic>>>uplink").Value() != want {
			t.Fatalf("wire progress differs from actual effect: %v", item)
		}
	}
	if failure.Progress.Items[0].Acknowledgement.PreviousBytes != 123 {
		t.Fatal("wire acknowledgement lost previous bytes")
	}
}
