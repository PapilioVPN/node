//go:build integration && linux

package bootstrap_test

import (
	"context"
	"fmt"
	"testing"

	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/PapilioVPN/node/internal/bootstrap"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

func assertRoutingMutationFailure(t *testing.T, err error, code codes.Code, outcome xrayv1.RoutingMutationOutcome) {
	t.Helper()
	if status.Code(err) != code {
		t.Fatalf("unexpected routing mutation status: %v want %v", err, code)
	}
	details := status.Convert(err).Details()
	if len(details) != 1 {
		t.Fatalf("routing mutation outcome detail missing: %v", err)
	}
	failure, ok := details[0].(*xrayv1.RoutingMutationFailure)
	if !ok || failure.Outcome != outcome {
		t.Fatalf("routing mutation effects misreported: %v", details)
	}
}

func TestRoutingManagementGrantOverMTLSIsIndependent(t *testing.T) {
	for _, granted := range []bool{false, true} {
		t.Run(fmt.Sprint(granted), func(t *testing.T) {
			certificates := testutil.NewCertificates(t)
			cfg := certificates.Config()
			p := &cfg.Communications.GRPC.Permissions
			p.ManageEngineRouting = granted
			p.ReadEngineInfo, p.ReadEngineStats, p.ReadEngineOnline, p.ReadEngineUsers, p.ReadEngineRouting, p.TestEngineRouting = !granted, !granted, !granted, !granted, !granted, !granted
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
			response, err := client.OverrideBalancerTarget(t.Context(), &xrayv1.OverrideBalancerTargetRequest{InstanceId: "missing", BalancerTag: "pool", Target: proto.String("")})
			if response != nil {
				t.Fatal("missing/unauthorized override returned success")
			}
			assertRoutingMutationFailure(t, err, code, xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
			_, err = client.OverrideBalancerTarget(t.Context(), &xrayv1.OverrideBalancerTargetRequest{})
			shapeCode := codes.PermissionDenied
			if granted {
				shapeCode = codes.InvalidArgument
			}
			assertRoutingMutationFailure(t, err, shapeCode, xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
			if granted {
				_, err = client.OverrideBalancerTarget(t.Context(), &xrayv1.OverrideBalancerTargetRequest{InstanceId: "missing", BalancerTag: "pool"})
				assertRoutingMutationFailure(t, err, codes.InvalidArgument, xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
				if _, err := client.ListRoutingRules(t.Context(), &xrayv1.ListRoutingRulesRequest{InstanceId: "missing"}); status.Code(err) != codes.PermissionDenied {
					t.Fatalf("write grant enabled routing reads: %v", err)
				}
				_, err := client.TestRoute(t.Context(), &xrayv1.TestRouteRequest{InstanceId: "missing", Context: &xrayv1.RoutingContext{}})
				assertRoutingProbeFailure(t, err, codes.PermissionDenied, xrayv1.RoutingProbeOutcome_ROUTING_PROBE_OUTCOME_NOT_EXECUTED)
				if _, err := client.RemoveInboundUser(t.Context(), &xrayv1.RemoveInboundUserRequest{}); status.Code(err) != codes.PermissionDenied {
					t.Fatalf("routing grant enabled user mutations: %v", err)
				}
			}
		})
	}
}
