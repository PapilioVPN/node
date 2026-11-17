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
	routingv1 "github.com/PapilioVPN/node/internal/modules/xray/routing/grpcapi/proto/v1"
	userv1 "github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/testutil"
)

func assertRoutingMutationFailure(t *testing.T, err error, code codes.Code, outcome routingv1.RoutingMutationOutcome) {
	t.Helper()
	if status.Code(err) != code {
		t.Fatalf("unexpected routing mutation status: %v want %v", err, code)
	}
	details := status.Convert(err).Details()
	if len(details) != 1 {
		t.Fatalf("routing mutation outcome detail missing: %v", err)
	}
	failure, ok := details[0].(*routingv1.RoutingMutationFailure)
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
			client := xrayservice.NewXrayServiceClient(certificates.GRPCClient(t, server.Addr().String()))
			code := codes.PermissionDenied
			if granted {
				code = codes.NotFound
			}
			response, err := client.OverrideBalancerTarget(t.Context(), &routingv1.OverrideBalancerTargetRequest{InstanceId: "missing", BalancerTag: "pool", Target: proto.String("")})
			if response != nil {
				t.Fatal("missing/unauthorized override returned success")
			}
			assertRoutingMutationFailure(t, err, code, routingv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)

			removed, removalError := client.RemoveRoutingRule(t.Context(), &routingv1.RemoveRoutingRuleRequest{InstanceId: "missing", RuleTag: "rule"})
			if removed != nil {
				t.Fatal("missing/unauthorized rule removal returned success")
			}
			assertRoutingMutationFailure(t, removalError, code, routingv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)

			added, additionError := client.AddRoutingRule(t.Context(), &routingv1.AddRoutingRuleRequest{InstanceId: "missing", RuleJson: []byte(`{"network":"tcp","outboundTag":"out"}`), Mode: routingv1.RoutingRuleMode_ROUTING_RULE_MODE_APPEND})
			if added != nil {
				t.Fatal("missing/unauthorized addition returned success")
			}
			assertRoutingMutationFailure(t, additionError, code, routingv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
			_, err = client.OverrideBalancerTarget(t.Context(), &routingv1.OverrideBalancerTargetRequest{})
			shapeCode := codes.PermissionDenied
			if granted {
				shapeCode = codes.InvalidArgument
			}
			assertRoutingMutationFailure(t, err, shapeCode, routingv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
			_, removalError = client.RemoveRoutingRule(t.Context(), &routingv1.RemoveRoutingRuleRequest{})
			assertRoutingMutationFailure(t, removalError, shapeCode, routingv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
			_, additionError = client.AddRoutingRule(t.Context(), &routingv1.AddRoutingRuleRequest{InstanceId: "missing", RuleJson: []byte(`{}`)})
			assertRoutingMutationFailure(t, additionError, shapeCode, routingv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)

			if granted {
				_, err = client.OverrideBalancerTarget(t.Context(), &routingv1.OverrideBalancerTargetRequest{InstanceId: "missing", BalancerTag: "pool"})
				assertRoutingMutationFailure(t, err, codes.InvalidArgument, routingv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
				if _, err := client.ListRoutingRules(t.Context(), &routingv1.ListRoutingRulesRequest{InstanceId: "missing"}); status.Code(err) != codes.PermissionDenied {
					t.Fatalf("write grant enabled routing reads: %v", err)
				}
				_, err := client.TestRoute(t.Context(), &routingv1.TestRouteRequest{InstanceId: "missing", Context: &routingv1.RoutingContext{}})
				assertRoutingProbeFailure(t, err, codes.PermissionDenied, routingv1.RoutingProbeOutcome_ROUTING_PROBE_OUTCOME_NOT_EXECUTED)
				if _, err := client.RemoveInboundUser(t.Context(), &userv1.RemoveInboundUserRequest{}); status.Code(err) != codes.PermissionDenied {
					t.Fatalf("routing grant enabled user mutations: %v", err)
				}
			}
		})
	}
}
