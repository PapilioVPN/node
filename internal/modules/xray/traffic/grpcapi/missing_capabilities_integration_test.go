//go:build integration && linux

package grpcapi_test

import (
	"context"
	"io"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/bootstrap"
	xrayv1 "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
	trafficv1 "github.com/PapilioVPN/node/internal/modules/xray/traffic/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

func TestTrafficReadsRejectUnsupportedNativeServiceOverMTLS(t *testing.T) {
	for _, network := range []string{"tcp", "unix"} {
		t.Run(network, func(t *testing.T) {
			certificates := testutil.NewCertificates(t)
			instance, health := testutil.ConfiguredXray(t, certificates.Directory, "edge")
			testutil.ConfigureXrayAPI(t, &instance, network, []string{})

			cfg := certificates.Config()
			cfg.Instances = []settings.Instance{instance}
			cfg.Communications.GRPC.Permissions.ReadEngineStats = true
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

			if result, err := client.GetTrafficCounter(ctx, &trafficv1.GetTrafficCounterRequest{
				InstanceId: "edge", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_INBOUND,
				Name: "proxy", Direction: trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK,
			}); result != nil || status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("absent native StatsService became zero usage: %v, %v", result, err)
			}
		})
	}
}
