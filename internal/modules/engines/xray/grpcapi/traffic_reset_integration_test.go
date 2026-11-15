//go:build integration

package grpcapi_test

import (
	"context"
	"net"
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

// The official native manager/service performs the actual swap. Only transport
// endpoints are isolated in-process; this does not claim a real Xray process.
func TestMTLSResetFailureCarriesUnknownOutcomeAfterNativeSwap(t *testing.T) {
	manager, err := nativestats.NewManager(t.Context(), &nativestats.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	counter, err := manager.RegisterCounter("user>>>user>>>traffic>>>uplink")
	if err != nil {
		t.Fatal(err)
	}
	counter.Set(123)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	nativeServer := grpc.NewServer()
	native.RegisterStatsServiceServer(nativeServer, native.NewStatsServer(manager))
	nativeDone := make(chan struct{})
	go func() { defer close(nativeDone); _ = nativeServer.Serve(listener) }()
	t.Cleanup(func() { nativeServer.Stop(); nativeServer.GracefulStop(); <-nativeDone })
	backend, err := api.NewClient(api.Config{Network: "tcp", Address: listener.Addr().String(), RequestTimeout: time.Second, MaxReceiveBytes: 1, MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	service := services.NewTrafficCounterResetService(map[instances.InstanceID]services.TrafficCounterResetter{"edge": backend})
	certificates := testutil.NewCertificates(t)
	management, err := grpcserver.NewServer(certificates.Config().Communications.GRPC, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = management.Close() })
	xrayv1.RegisterXrayServiceServer(management.Registrar(), grpcapi.Handler{AllowStatsReset: true, Resets: service})
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
	result, err := xrayv1.NewXrayServiceClient(connection).ResetTrafficCounter(request, resetRequest())
	if result != nil || status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("oversized native acknowledgement became successful: %v %v", result, err)
	}
	details := status.Convert(err).Details()
	if len(details) != 1 {
		t.Fatalf("unknown-effect detail did not cross real mTLS: %v", err)
	}
	failure, ok := details[0].(*xrayv1.TrafficCounterResetFailure)
	if !ok || failure.Outcome != xrayv1.TrafficCounterResetOutcome_TRAFFIC_COUNTER_RESET_OUTCOME_UNKNOWN {
		t.Fatalf("wire falsely asserted no effect: %v", details)
	}
	if counter.Value() != 0 {
		t.Fatal("native reset did not actually apply before response rejection")
	}
}
