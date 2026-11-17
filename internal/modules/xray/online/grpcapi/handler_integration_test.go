//go:build integration && linux

package grpcapi_test

import (
	"context"
	"testing"

	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/bootstrap"
	xrayv1 "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
	onlinev1 "github.com/PapilioVPN/node/internal/modules/xray/online/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

func TestOnlineRPCsRejectExistingTrafficAndInfoGrants(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	cfg := certificates.Config()
	cfg.Communications.GRPC.Permissions.ReadEngineInfo = true
	cfg.Communications.GRPC.Permissions.ReadEngineStats = true
	var server *rpc.Server
	application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&server))
	if err := application.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := application.Stop(context.Background()); err != nil {
			t.Error(err)
		}
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	})
	client := xrayv1.NewXrayServiceClient(certificates.GRPCClient(t, server.Addr().String()))
	_, countError := client.GetUserOnlineStats(t.Context(), &onlinev1.GetUserOnlineStatsRequest{})
	_, ipsError := client.ListUserOnlineIPs(t.Context(), &onlinev1.ListUserOnlineIPsRequest{})
	_, usersError := client.ListOnlineUsers(t.Context(), &onlinev1.ListOnlineUsersRequest{})
	for _, err := range []error{countError, ipsError, usersError} {
		if status.Code(err) != codes.PermissionDenied {
			t.Errorf("existing grants reached online observations: %v", err)
		}
	}
}
