//go:build integration && linux

package bootstrap_test

import (
	"context"
	"fmt"
	"testing"

	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/bootstrap"
	routingv1 "github.com/PapilioVPN/node/internal/modules/core/routing/grpcapi/proto/v1"
	userv1 "github.com/PapilioVPN/node/internal/modules/core/users/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

func assertRoutingProbeFailure(t *testing.T, err error, code codes.Code, outcome routingv1.RoutingProbeOutcome) {
	t.Helper()
	if status.Code(err) != code {
		t.Fatalf("unexpected routing probe status: %v want %v", err, code)
	}
	details := status.Convert(err).Details()
	if len(details) != 1 {
		t.Fatalf("routing probe outcome detail missing: %v", err)
	}
	failure, ok := details[0].(*routingv1.TestRouteFailure)
	if !ok || failure.Outcome != outcome {
		t.Fatalf("routing probe effects misreported: %v", details)
	}
}

func TestRoutingProbeGrantOverMTLSIsIndependent(t *testing.T) {
	for _, granted := range []bool{false, true} {
		t.Run(fmt.Sprint(granted), func(t *testing.T) {
			certificates := testutil.NewCertificates(t)
			cfg := certificates.Config()
			p := &cfg.Communications.GRPC.Permissions
			p.TestEngineRouting = granted
			p.ManageEngineRouting = !granted
			p.ReadEngineInfo, p.ReadEngineStats, p.ReadEngineOnline, p.ReadEngineUsers, p.ReadEngineRouting = !granted, !granted, !granted, !granted, !granted
			p.ReadInstanceStatus, p.ControlInstances, p.ValidateInstanceConfig, p.ResetEngineStats, p.ManageEngineUsers = !granted, !granted, !granted, !granted, !granted
			var server *rpc.Server
			application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&server))
			if err := application.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = application.Stop(context.Background()); _ = server.Close() })
			client := xrayClient(t, server, certificates)
			code := codes.PermissionDenied
			if granted {
				code = codes.NotFound
			}
			response, err := client.TestRoute(t.Context(), &routingv1.TestRouteRequest{InstanceId: "missing", Context: &routingv1.RoutingContext{}})
			if response != nil {
				t.Fatal("missing/unauthorized probe returned facts")
			}
			assertRoutingProbeFailure(t, err, code, routingv1.RoutingProbeOutcome_ROUTING_PROBE_OUTCOME_NOT_EXECUTED)
			if !granted {
				_, err := client.TestRoute(t.Context(), &routingv1.TestRouteRequest{})
				assertRoutingProbeFailure(t, err, codes.PermissionDenied, routingv1.RoutingProbeOutcome_ROUTING_PROBE_OUTCOME_NOT_EXECUTED)
			} else {
				_, err := client.TestRoute(t.Context(), &routingv1.TestRouteRequest{InstanceId: "missing", Context: &routingv1.RoutingContext{TargetPort: 65536}})
				assertRoutingProbeFailure(t, err, codes.InvalidArgument, routingv1.RoutingProbeOutcome_ROUTING_PROBE_OUTCOME_NOT_EXECUTED)
				if _, err := client.ListRoutingRules(t.Context(), &routingv1.ListRoutingRulesRequest{InstanceId: "missing"}); status.Code(err) != codes.PermissionDenied {
					t.Fatalf("probe grant enabled routing reads: %v", err)
				}
				if _, err := client.RemoveInboundUser(t.Context(), &userv1.RemoveInboundUserRequest{}); status.Code(err) != codes.PermissionDenied {
					t.Fatalf("probe grant enabled user mutations: %v", err)
				}
			}
		})
	}
}
