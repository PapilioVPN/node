//go:build integration && linux

package bootstrap_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/PapilioVPN/node/internal/bootstrap"
	xrayv1 "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
	"github.com/PapilioVPN/node/internal/modules/core/instances"
	instancesv1 "github.com/PapilioVPN/node/internal/modules/core/instances/grpcapi/proto/v1"
	infov1 "github.com/PapilioVPN/node/internal/modules/xray/info/grpcapi/proto/v1"
	outboundsv1 "github.com/PapilioVPN/node/internal/modules/xray/outbounds/grpcapi/proto/v1"
	routingv1 "github.com/PapilioVPN/node/internal/modules/xray/routing/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

func xrayClient(t *testing.T, server *rpc.Server, certificates testutil.Certificates) xrayv1.XrayServiceClient {
	t.Helper()
	connection, err := grpc.NewClient(server.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(certificates.ClientTLS(t))))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return xrayv1.NewXrayServiceClient(connection)
}

func TestXrayBinaryInfoOverMTLSKeepsLiveInstancesUnchanged(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	first, firstAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-a")
	second, secondAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-b")
	first.Xray.Inspection = &settings.BinaryInspection{Timeout: time.Second, MaxOutputBytes: 1024, MaxConcurrent: 1}
	original, err := os.ReadFile(first.Xray.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg := certificates.Config()
	cfg.Instances = []settings.Instance{first, second}
	cfg.Communications.GRPC.Permissions.ReadEngineInfo = true
	cfg.Communications.GRPC.Permissions.ReadEngineOutbounds = true
	cfg.Communications.GRPC.Permissions.ReadEngineUserMetadata = true
	cfg.Communications.GRPC.Permissions.ReadEngineUserAccounts = true
	cfg.Communications.GRPC.Permissions.TestEngineRouting = true
	cfg.Communications.GRPC.Permissions.ManageEngineRouting = true
	var server *rpc.Server
	var resources *instances.Resources
	application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&server, &resources))
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
		if err := resources.Close(); err != nil {
			t.Error(err)
		}
		testutil.AssertTCPPortsReleased(t, firstAddress, secondAddress)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	if err := testutil.XrayTraffic(ctx, firstAddress, secondAddress); err != nil {
		t.Fatal(err)
	}
	client := xrayClient(t, server, certificates)
	if value, err := client.ListOutboundStatuses(ctx, &outboundsv1.ListOutboundStatusesRequest{InstanceId: "edge-a"}); value != nil || status.Code(err) != codes.FailedPrecondition {
		t.Fatal("disabled API became successful outbound collection")
	}
	if value, err := client.GetOutboundStatus(ctx, &outboundsv1.GetOutboundStatusRequest{InstanceId: "edge-a", OutboundTag: proto.String("out")}); value != nil || status.Code(err) != codes.FailedPrecondition {
		t.Fatal("disabled API became absent outbound observation")
	}
	_, probeError := client.TestRoute(ctx, &routingv1.TestRouteRequest{InstanceId: "edge-a", Context: &routingv1.RoutingContext{}})
	assertRoutingProbeFailure(t, probeError, codes.FailedPrecondition, routingv1.RoutingProbeOutcome_ROUTING_PROBE_OUTCOME_NOT_EXECUTED)
	_, overrideError := client.OverrideBalancerTarget(ctx, &routingv1.OverrideBalancerTargetRequest{InstanceId: "edge-a", BalancerTag: "pool", Target: proto.String("")})
	assertRoutingMutationFailure(t, overrideError, codes.FailedPrecondition, routingv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
	_, removalError := client.RemoveRoutingRule(ctx, &routingv1.RemoveRoutingRuleRequest{InstanceId: "edge-a", RuleTag: "rule"})
	assertRoutingMutationFailure(t, removalError, codes.FailedPrecondition, routingv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
	_, additionError := client.AddRoutingRule(ctx, &routingv1.AddRoutingRuleRequest{InstanceId: "edge-a", RuleJson: []byte(`{"network":"tcp","outboundTag":"out"}`), Mode: routingv1.RoutingRuleMode_ROUTING_RULE_MODE_APPEND})
	assertRoutingMutationFailure(t, additionError, codes.FailedPrecondition, routingv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)

	if _, err := client.GetRuntimeInfo(ctx, &infov1.GetRuntimeInfoRequest{InstanceId: "edge-a"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("absent API settings were ignored: %v", err)
	}
	before := time.Now().UnixMilli()
	response, err := client.GetBinaryInfo(ctx, &infov1.GetBinaryInfoRequest{InstanceId: "edge-a"})
	if err != nil {
		t.Fatal(err)
	}
	info := response.GetBinary()
	if response.InstanceId != "edge-a" || info.GetVersion() != "26.11.9" || info.Build == "" || !strings.HasPrefix(info.GoVersion, "go") || info.OperatingSystem != "linux" || info.Architecture != "amd64" || info.ObservedAtUnixMs < before || info.ObservedAtUnixMs > time.Now().UnixMilli() {
		t.Fatalf("native Xray binary facts over mTLS: %v", response)
	}
	for id, expected := range map[string]codes.Code{"missing": codes.NotFound, "edge-b": codes.FailedPrecondition, " edge-a ": codes.NotFound, "": codes.InvalidArgument} {
		if _, err := client.GetBinaryInfo(ctx, &infov1.GetBinaryInfoRequest{InstanceId: id}); status.Code(err) != expected || strings.Contains(err.Error(), certificates.Directory) {
			t.Fatalf("selection failure: %v; want %v", err, expected)
		}
	}
	controls := instanceClient(t, server, certificates)
	if _, err := controls.GetInstanceStatus(ctx, &instancesv1.GetInstanceStatusRequest{InstanceId: "edge-a"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("engine information granted status reads: %v", err)
	}
	if _, err := controls.StopInstance(ctx, &instancesv1.StopInstanceRequest{InstanceId: "edge-a"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("engine information granted runtime control: %v", err)
	}
	if _, err := controls.ValidateInstanceConfig(ctx, &instancesv1.ValidateInstanceConfigRequest{InstanceId: "edge-a"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("engine information granted config validation: %v", err)
	}
	if err := testutil.XrayTraffic(ctx, firstAddress, secondAddress); err != nil {
		t.Fatalf("binary inspection interrupted traffic: %v", err)
	}
	current, err := os.ReadFile(first.Xray.ConfigFile)
	if err != nil || !bytes.Equal(current, original) {
		t.Fatalf("binary inspection modified native configuration: %v", err)
	}
}

func TestXrayBinaryInfoRequiresItsOwnGrant(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	cfg := certificates.Config()
	cfg.Communications.GRPC.Permissions.ReadInstanceStatus = true
	cfg.Communications.GRPC.Permissions.ControlInstances = true
	cfg.Communications.GRPC.Permissions.ValidateInstanceConfig = true
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
	client := xrayClient(t, server, certificates)
	for _, id := range []string{"unknown", ""} {
		if _, err := client.GetBinaryInfo(t.Context(), &infov1.GetBinaryInfoRequest{InstanceId: id}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("other instance grants must not authorize engine information: %v", err)
		}
		if _, err := client.GetRuntimeInfo(t.Context(), &infov1.GetRuntimeInfoRequest{InstanceId: id}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("other instance grants must not authorize runtime information: %v", err)
		}
	}
}
