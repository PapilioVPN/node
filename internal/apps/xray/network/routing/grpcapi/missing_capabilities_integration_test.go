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

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
	routingv1 "github.com/PapilioVPN/node/internal/apps/xray/network/routing/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/bootstrap"
	xrayv1 "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestRoutingOperationsRejectMissingNativeCapabilitiesOverMTLS(t *testing.T) {
	for _, network := range []string{"tcp", "unix"} {
		for _, mode := range []string{"disabled", "unsupported", "preparation-disabled"} {
			t.Run(network+"/"+mode, func(t *testing.T) {
				certificates := testutil.NewCertificates(t)
				instance, health := testutil.ConfiguredXray(t, certificates.Directory, "edge")
				if mode != "disabled" {
					testutil.ConfigureXrayAPI(t, &instance, network, []string{})
				}
				if mode == "unsupported" {
					instance.Xray.RoutingRules = &settings.RoutingRulePreparation{WorkDir: t.TempDir(), MaxInputBytes: 1024, MaxOutputBytes: 65536, MaxConcurrent: 1}
				}
				cfg := certificates.Config()
				cfg.Instances = []settings.Instance{instance}
				cfg.Communications.GRPC.Permissions.ReadEngineRouting = true
				cfg.Communications.GRPC.Permissions.TestEngineRouting = true
				cfg.Communications.GRPC.Permissions.ManageEngineRouting = true
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

				if mode != "disabled" {
					oracle := nativeXrayAPIClient(t, instance)
					if result, err := oracle.TestRoute(ctx, domain.RoutingProbe{}); !errors.Is(err, domain.ErrRoutingProbeUnsupported) || errors.Is(err, domain.ErrRoutingProbeUnknown) || !result.AcknowledgedAt.IsZero() {
						t.Fatalf("absent native RoutingService accepted a probe or claimed effects: %+v %v", result, err)
					}
					if ack, err := oracle.OverrideBalancerTarget(ctx, domain.BalancerOverride{BalancerTag: "pool"}); !errors.Is(err, domain.ErrRoutingManagementUnsupported) || errors.Is(err, domain.ErrRoutingMutationUnknown) || !ack.AcknowledgedAt.IsZero() {
						t.Fatalf("absent native RoutingService accepted an override/claimed effects: %+v %v", ack, err)
					}
				}
				_, probeError := client.TestRoute(ctx, &routingv1.TestRouteRequest{InstanceId: "edge", Context: &routingv1.RoutingContext{}})
				assertRoutingProbeFailure(t, probeError, codes.FailedPrecondition, routingv1.RoutingProbeOutcome_ROUTING_PROBE_OUTCOME_NOT_EXECUTED)
				_, overrideError := client.OverrideBalancerTarget(ctx, &routingv1.OverrideBalancerTargetRequest{InstanceId: "edge", BalancerTag: "pool", Target: proto.String("")})
				assertRoutingMutationFailure(t, overrideError, codes.FailedPrecondition, routingv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
				_, removalError := client.RemoveRoutingRule(ctx, &routingv1.RemoveRoutingRuleRequest{InstanceId: "edge", RuleTag: "rule"})
				assertRoutingMutationFailure(t, removalError, codes.FailedPrecondition, routingv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
				_, additionError := client.AddRoutingRule(ctx, &routingv1.AddRoutingRuleRequest{InstanceId: "edge", RuleJson: []byte(`{"network":"tcp","outboundTag":"out"}`), Mode: routingv1.RoutingRuleMode_ROUTING_RULE_MODE_APPEND})
				assertRoutingMutationFailure(t, additionError, codes.FailedPrecondition, routingv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
				if result, err := client.ListRoutingRules(ctx, &routingv1.ListRoutingRulesRequest{InstanceId: "edge"}); result != nil || status.Code(err) != codes.FailedPrecondition {
					t.Fatalf("absent native RoutingService became successful rules: %v %v", result, err)
				}
				if result, err := client.GetBalancerInfo(ctx, &routingv1.GetBalancerInfoRequest{InstanceId: "edge", BalancerTag: "pool"}); result != nil || status.Code(err) != codes.FailedPrecondition {
					t.Fatalf("absent native RoutingService became successful balancer: %v %v", result, err)
				}

			})
		}
	}
}
