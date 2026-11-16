//go:build integration && linux

package grpcapi_test

import (
	"context"
	"fmt"
	"testing"

	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/PapilioVPN/node/internal/bootstrap"
	xrayservice "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/xray/outbounds/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

func TestOutboundReadGrantOverMTLSIsIndependent(t *testing.T) {
	for _, granted := range []bool{false, true} {
		t.Run(fmt.Sprint(granted), func(t *testing.T) {
			certificates := testutil.NewCertificates(t)
			cfg := certificates.Config()
			p := &cfg.Communications.GRPC.Permissions
			p.ReadEngineOutbounds = granted
			p.ReadEngineInfo, p.ReadEngineStats, p.ReadEngineOnline, p.ReadEngineUsers, p.ReadEngineRouting, p.TestEngineRouting, p.ManageEngineRouting = !granted, !granted, !granted, !granted, !granted, !granted, !granted
			p.ReadInstanceStatus, p.ControlInstances, p.ValidateInstanceConfig, p.ResetEngineStats, p.ManageEngineUsers = !granted, !granted, !granted, !granted, !granted
			var server *rpc.Server
			application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&server))
			if err := application.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = application.Stop(context.Background()); _ = server.Close() })
			client := xrayservice.NewXrayServiceClient(certificates.GRPCClient(t, server.Addr().String()))
			code := codes.PermissionDenied
			if granted {
				code = codes.NotFound
			}
			if result, err := client.GetOutboundStatus(t.Context(), &xrayv1.GetOutboundStatusRequest{InstanceId: "missing", OutboundTag: proto.String("")}); result != nil || status.Code(err) != code {
				t.Fatalf("mTLS single grant/lookup differs: %v %v", result, err)
			}
			if result, err := client.ListOutboundStatuses(t.Context(), &xrayv1.ListOutboundStatusesRequest{InstanceId: "missing"}); result != nil || status.Code(err) != code {
				t.Fatalf("mTLS collection grant/lookup differs: %v %v", result, err)
			}
			shape := codes.PermissionDenied
			if granted {
				shape = codes.InvalidArgument
			}
			if _, err := client.GetOutboundStatus(t.Context(), &xrayv1.GetOutboundStatusRequest{InstanceId: "missing"}); status.Code(err) != shape {
				t.Fatal("single shape preceded mTLS grant or lost tag presence")
			}
			if _, err := client.ListOutboundStatuses(t.Context(), &xrayv1.ListOutboundStatusesRequest{}); status.Code(err) != shape {
				t.Fatal("collection shape preceded mTLS grant")
			}
		})
	}
}
