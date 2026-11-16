//go:build integration

package grpcapi_test

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	native "github.com/xtls/xray-core/app/proxyman/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	grpcapi "github.com/PapilioVPN/node/internal/bootstrap/xrayapi"
	xrayv1 "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/users/app/services"
	usergrpc "github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi"
	userv1 "github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi/proto/v1"
	userxray "github.com/PapilioVPN/node/internal/modules/xray/users/infra/api"
	"github.com/PapilioVPN/node/internal/platform/grpcserver"
	nativeapi "github.com/PapilioVPN/node/internal/platform/xray"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

// This controlled native server models a removal followed by a lost reply.
// Real Xray authentication effects are verified in user_remove_native_integration_test.go.
type lostUserRemovalServer struct {
	native.UnimplementedHandlerServiceServer
	removed atomic.Bool
	calls   atomic.Int32
}

func (s *lostUserRemovalServer) AlterInbound(_ context.Context, r *native.AlterInboundRequest) (*native.AlterInboundResponse, error) {
	s.calls.Add(1)
	operation, err := r.Operation.GetInstance()
	if err != nil {
		return nil, err
	}
	remove, ok := operation.(*native.RemoveUserOperation)
	if !ok || r.Tag != "in" || remove.Email != "user" {
		return nil, status.Error(codes.InvalidArgument, "wrong native selection")
	}
	s.removed.Store(true)
	return nil, status.Error(codes.Unavailable, "private reply lost after removal")
}

func TestMTLSUserRemovalFailureRetainsUnknownEffectWithoutReplay(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	backend := &lostUserRemovalServer{}
	nativeServer := grpc.NewServer()
	native.RegisterHandlerServiceServer(nativeServer, backend)
	done := make(chan struct{})
	go func() { defer close(done); _ = nativeServer.Serve(listener) }()
	t.Cleanup(func() { nativeServer.Stop(); nativeServer.GracefulStop(); <-done })
	channel, err := nativeapi.NewClient(nativeapi.Config{Network: "tcp", Address: listener.Addr().String(), RequestTimeout: time.Second, MaxReceiveBytes: 4096, MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = channel.Close() })
	service := services.NewUserRemovalService(map[instances.InstanceID]services.InboundUserRemover{"edge": &userxray.Adapter{API: channel}})
	certificates := testutil.NewCertificates(t)
	management, err := grpcserver.NewServer(certificates.Config().Communications.GRPC, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = management.Close() })
	xrayv1.RegisterXrayServiceServer(management.Registrar(), grpcapi.Handler{UserAPI: usergrpc.UserAPI{AllowUserManagement: true, UserRemoval: service}})
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
	response, err := xrayv1.NewXrayServiceClient(connection).RemoveInboundUser(ctx, &userv1.RemoveInboundUserRequest{InstanceId: "edge", InboundTag: "in", Email: "user"})
	if response != nil || status.Code(err) != codes.Unavailable || !backend.removed.Load() || backend.calls.Load() != 1 {
		t.Fatalf("lost native effect changed/retried: %v %v calls=%d", response, err, backend.calls.Load())
	}
	details := status.Convert(err).Details()
	if len(details) != 1 {
		t.Fatalf("missing effect detail: %v", err)
	}
	failure, ok := details[0].(*userv1.UserMutationFailure)
	if !ok || failure.Outcome != userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_UNKNOWN {
		t.Fatalf("applied removal was labeled not applied: %v", details)
	}
}
