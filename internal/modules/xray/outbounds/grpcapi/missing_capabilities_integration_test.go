//go:build integration && linux

package grpcapi_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/PapilioVPN/node/internal/bootstrap"
	xrayv1 "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
	"github.com/PapilioVPN/node/internal/modules/xray/outbounds/domain"
	outboundsv1 "github.com/PapilioVPN/node/internal/modules/xray/outbounds/grpcapi/proto/v1"
	outboundapi "github.com/PapilioVPN/node/internal/modules/xray/outbounds/infra/api"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestOutboundObservationsRejectMissingNativeCapabilitiesOverMTLS(t *testing.T) {
	for _, network := range []string{"tcp", "unix"} {
		for _, mode := range []string{"disabled", "unsupported"} {
			t.Run(network+"/"+mode, func(t *testing.T) {
				certificates := testutil.NewCertificates(t)
				instance, health := testutil.ConfiguredXray(t, certificates.Directory, "edge")
				if mode != "disabled" {
					testutil.ConfigureXrayAPI(t, &instance, network, []string{})
				}

				cfg := certificates.Config()
				cfg.Instances = []settings.Instance{instance}
				cfg.Communications.GRPC.Permissions.ReadEngineOutbounds = true
				var server *rpc.Server
				var cleanup struct {
					fx.In
					Closers []io.Closer `group:"module_resources"`
				}
				application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&server, &cleanup))
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
					for _, closer := range cleanup.Closers {
						if err := closer.Close(); err != nil {
							t.Error(err)
						}
					}
					testutil.AssertTCPPortsReleased(t, health)
				})
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				if err := testutil.XrayTraffic(ctx, health); err != nil {
					t.Fatal(err)
				}
				client := xrayv1.NewXrayServiceClient(certificates.GRPCClient(t, server.Addr().String()))

				if value, err := client.ListOutboundStatuses(ctx, &outboundsv1.ListOutboundStatusesRequest{InstanceId: "edge"}); value != nil || status.Code(err) != codes.FailedPrecondition {
					t.Fatal("disabled API became successful outbound collection")
				}
				if value, err := client.GetOutboundStatus(ctx, &outboundsv1.GetOutboundStatusRequest{InstanceId: "edge", OutboundTag: proto.String("out")}); value != nil || status.Code(err) != codes.FailedPrecondition {
					t.Fatal("disabled API became absent outbound observation")
				}
				if mode != "disabled" {
					nativeClient := testutil.XrayAPIClient(t, instance.Xray.API)
					observer := &outboundapi.Observer{API: nativeClient}
					if result, err := observer.ReadOutboundStatuses(ctx); !errors.Is(err, domain.ErrOutboundObservationUnsupported) || result.Items != nil || !result.ObservedAt.IsZero() {
						t.Fatalf("absent native ObservatoryService became empty successful observations: %+v %v", result, err)
					}
				}

			})
		}
	}
}
