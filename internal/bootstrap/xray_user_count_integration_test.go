//go:build integration

package bootstrap_test

import (
	"context"
	"fmt"
	"testing"

	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/bootstrap"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/xray/grpcapi/proto/v1"
	userv1 "github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

func TestUserCountRequiresIndependentGrantOverMTLS(t *testing.T) {
	for _, granted := range []bool{false, true} {
		t.Run(fmt.Sprint(granted), func(t *testing.T) {
			certificates := testutil.NewCertificates(t)
			cfg := certificates.Config()
			p := &cfg.Communications.GRPC.Permissions
			p.ReadEngineUsers = granted
			p.ManageEngineUsers, p.ReadEngineInfo, p.ReadEngineStats, p.ReadEngineOnline = !granted, !granted, !granted, !granted
			p.ReadInstanceStatus, p.ControlInstances, p.ValidateInstanceConfig, p.ResetEngineStats = !granted, !granted, !granted, !granted
			var server *rpc.Server
			application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&server))
			if err := application.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = application.Stop(context.Background()); _ = server.Close() })
			client := xrayClient(t, server, certificates)
			response, err := client.GetInboundUserCount(t.Context(), &userv1.GetInboundUserCountRequest{InstanceId: "missing", InboundTag: "in"})
			want := codes.PermissionDenied
			if granted {
				want = codes.NotFound
			}
			if response != nil || status.Code(err) != want {
				t.Fatalf("count grant/lookup boundary changed: %v %v", response, err)
			}
			if granted {
				if _, err := client.RemoveInboundUser(t.Context(), &userv1.RemoveInboundUserRequest{InstanceId: "missing", InboundTag: "in", Email: "user"}); status.Code(err) != codes.PermissionDenied {
					t.Fatalf("count read enabled user mutations: %v", err)
				}
				if _, err := client.GetUserOnlineStats(t.Context(), &xrayv1.GetUserOnlineStatsRequest{InstanceId: "missing", Email: "user"}); status.Code(err) != codes.PermissionDenied {
					t.Fatalf("count read enabled online observations: %v", err)
				}
			}
		})
	}
}
