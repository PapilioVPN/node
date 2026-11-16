//go:build integration

package observations_test

import (
	"context"
	"fmt"
	"testing"

	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/bootstrap"
	xrayv1 "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
	userv1 "github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

func TestUserObservationGrantsStayIndependentOverMTLS(t *testing.T) {
	for _, metadata := range []bool{false, true} {
		for _, accounts := range []bool{false, true} {
			t.Run(fmt.Sprintf("metadata=%t/accounts=%t", metadata, accounts), func(t *testing.T) {
				certificates := testutil.NewCertificates(t)
				cfg := certificates.Config()
				p := &cfg.Communications.GRPC.Permissions
				p.ReadEngineUserMetadata, p.ReadEngineUserAccounts = metadata, accounts
				prior := !metadata && !accounts
				p.ReadInstanceStatus, p.ControlInstances, p.ValidateInstanceConfig = prior, prior, prior
				p.ReadEngineInfo, p.ReadEngineStats, p.ResetEngineStats = prior, prior, prior
				p.ManageEngineUsers, p.ReadEngineUsers, p.ReadEngineOnline = prior, prior, prior
				p.ReadEngineRouting, p.ReadEngineOutbounds, p.TestEngineRouting, p.ManageEngineRouting = prior, prior, prior, prior
				var server *rpc.Server
				app := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&server))
				if err := app.Start(t.Context()); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := app.Stop(context.Background()); err != nil {
						t.Error(err)
					}
					_ = server.Close()
				})
				client := xrayv1.NewXrayServiceClient(certificates.GRPCClient(t, server.Addr().String()))
				_, e1 := client.GetInboundUserMetadata(t.Context(), &userv1.GetInboundUserMetadataRequest{InstanceId: "missing", InboundTag: "in", Email: "user"})
				_, e2 := client.ListInboundUserMetadata(t.Context(), &userv1.ListInboundUserMetadataRequest{InstanceId: "missing", InboundTag: "in"})
				_, e3 := client.GetInboundUser(t.Context(), &userv1.GetInboundUserRequest{InstanceId: "missing", InboundTag: "in", Email: "user"})
				_, e4 := client.ListInboundUsers(t.Context(), &userv1.ListInboundUsersRequest{InstanceId: "missing", InboundTag: "in"})
				for i, err := range []error{e1, e2, e3, e4} {
					allowed := metadata
					if i >= 2 {
						allowed = accounts
					}
					want := codes.PermissionDenied
					if allowed {
						want = codes.NotFound
					}
					if status.Code(err) != want {
						t.Fatalf("operation %d expanded another grant or was not bound: %v", i, err)
					}
				}
				if !prior {
					_, err := client.GetInboundUserCount(t.Context(), &userv1.GetInboundUserCountRequest{InstanceId: "missing", InboundTag: "in"})
					if status.Code(err) != codes.PermissionDenied {
						t.Fatal("user views enabled counts")
					}
					_, err = client.RemoveInboundUser(t.Context(), &userv1.RemoveInboundUserRequest{InstanceId: "missing", InboundTag: "in", Email: "user"})
					if status.Code(err) != codes.PermissionDenied {
						t.Fatal("user views enabled mutations")
					}
				}
			})
		}
	}
}
