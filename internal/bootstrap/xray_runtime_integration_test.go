//go:build integration && linux

package bootstrap_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/modules/core/instances"
	instancesv1 "github.com/PapilioVPN/node/internal/modules/core/instances/grpcapi/proto/v1"
	xraydomain "github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

func TestXrayRuntimeInfoOverMTLSAndNativeReconnect(t *testing.T) {
	for _, network := range []string{"unix", "tcp"} {
		t.Run(network, func(t *testing.T) {
			certificates := testutil.NewCertificates(t)
			first, firstAddress := configuredXray(t, certificates.Directory, "edge-a")
			second, secondAddress := configuredXray(t, certificates.Directory, "edge-b")
			configureXrayAPI(t, &first, network, []string{"StatsService"})
			// A real API without StatsService must not become successful empty facts.
			configureXrayAPI(t, &second, network, []string{})
			second.Xray.RoutingRules = &settings.RoutingRulePreparation{WorkDir: t.TempDir(), MaxInputBytes: 1024, MaxOutputBytes: 65536, MaxConcurrent: 1}
			original, err := os.ReadFile(first.Xray.ConfigFile)
			if err != nil {
				t.Fatal(err)
			}
			cfg := certificates.Config()
			cfg.Instances = []settings.Instance{first, second}
			cfg.Communications.GRPC.Permissions.ReadEngineInfo = true
			cfg.Communications.GRPC.Permissions.ReadEngineStats = true
			cfg.Communications.GRPC.Permissions.ReadEngineUsers = true
			cfg.Communications.GRPC.Permissions.ReadEngineRouting = true
			cfg.Communications.GRPC.Permissions.TestEngineRouting = true
			cfg.Communications.GRPC.Permissions.ManageEngineRouting = true
			cfg.Communications.GRPC.Permissions.ControlInstances = true
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
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
				assertInstancePortsReleased(t, firstAddress, secondAddress)
				if network == "tcp" {
					assertInstancePortsReleased(t, first.Xray.API.Address, second.Xray.API.Address)
				}
			})
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if err := xrayTraffic(ctx, firstAddress, secondAddress); err != nil {
				t.Fatal(err)
			}
			client := xrayClient(t, server, certificates)
			response := awaitRuntimeInfo(t, ctx, client, "edge-a")
			if result, err := nativeXrayAPIClient(t, second).ReadOutboundStatuses(ctx); !errors.Is(err, xraydomain.ErrOutboundObservationUnsupported) || result.Items != nil || !result.ObservedAt.IsZero() {
				t.Fatalf("absent native ObservatoryService became empty successful observations: %+v %v", result, err)
			}
			if result, err := nativeXrayAPIClient(t, second).TestRoute(ctx, xraydomain.RoutingProbe{}); !errors.Is(err, xraydomain.ErrRoutingProbeUnsupported) || errors.Is(err, xraydomain.ErrRoutingProbeUnknown) || !result.AcknowledgedAt.IsZero() {
				t.Fatalf("absent native RoutingService accepted a probe or claimed effects: %+v %v", result, err)
			}
			if ack, err := nativeXrayAPIClient(t, second).OverrideBalancerTarget(ctx, xraydomain.BalancerOverride{BalancerTag: "pool"}); !errors.Is(err, xraydomain.ErrRoutingManagementUnsupported) || errors.Is(err, xraydomain.ErrRoutingMutationUnknown) || !ack.AcknowledgedAt.IsZero() {
				t.Fatalf("absent native RoutingService accepted an override/claimed effects: %+v %v", ack, err)
			}
			_, probeError := client.TestRoute(ctx, &xrayv1.TestRouteRequest{InstanceId: "edge-b", Context: &xrayv1.RoutingContext{}})
			assertRoutingProbeFailure(t, probeError, codes.FailedPrecondition, xrayv1.RoutingProbeOutcome_ROUTING_PROBE_OUTCOME_NOT_EXECUTED)
			_, overrideError := client.OverrideBalancerTarget(ctx, &xrayv1.OverrideBalancerTargetRequest{InstanceId: "edge-b", BalancerTag: "pool", Target: proto.String("")})
			assertRoutingMutationFailure(t, overrideError, codes.FailedPrecondition, xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
			_, removalError := client.RemoveRoutingRule(ctx, &xrayv1.RemoveRoutingRuleRequest{InstanceId: "edge-b", RuleTag: "rule"})
			assertRoutingMutationFailure(t, removalError, codes.FailedPrecondition, xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
			for _, id := range []string{"edge-a", "edge-b"} {
				_, additionError := client.AddRoutingRule(ctx, &xrayv1.AddRoutingRuleRequest{InstanceId: id, RuleJson: []byte(`{"network":"tcp","outboundTag":"out"}`), Mode: xrayv1.RoutingRuleMode_ROUTING_RULE_MODE_APPEND})
				assertRoutingMutationFailure(t, additionError, codes.FailedPrecondition, xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
			}

			if result, err := client.ListRoutingRules(ctx, &xrayv1.ListRoutingRulesRequest{InstanceId: "edge-b"}); result != nil || status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("absent native RoutingService became successful rules: %v %v", result, err)
			}
			if result, err := client.GetBalancerInfo(ctx, &xrayv1.GetBalancerInfoRequest{InstanceId: "edge-b", BalancerTag: "pool"}); result != nil || status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("absent native RoutingService became successful balancer: %v %v", result, err)
			}
			if result, err := client.GetInboundUserCount(ctx, &xrayv1.GetInboundUserCountRequest{InstanceId: "edge-b", InboundTag: "in"}); result != nil || status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("absent native HandlerService became successful count: %v %v", result, err)
			}
			if result, err := client.GetTrafficCounter(ctx, &xrayv1.GetTrafficCounterRequest{
				InstanceId: "edge-b", Scope: xrayv1.TrafficScope_TRAFFIC_SCOPE_INBOUND,
				Name: "proxy", Direction: xrayv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK,
			}); result != nil || status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("absent native StatsService became zero usage: %v, %v", result, err)
			}
			info := response.GetRuntime()
			if response.InstanceId != "edge-a" || info.GetGoroutines() == 0 || info.AllocatedBytes == 0 || info.SysBytes < info.AllocatedBytes || info.Mallocs < info.Frees || info.LiveObjects != info.Mallocs-info.Frees || info.ObservedAtUnixMs <= 0 {
				t.Fatalf("native runtime facts: %v", response)
			}
			for id, expected := range map[string]codes.Code{"missing": codes.NotFound, " edge-a ": codes.NotFound, "": codes.InvalidArgument, "edge-b": codes.FailedPrecondition} {
				if _, err := client.GetRuntimeInfo(ctx, &xrayv1.GetRuntimeInfoRequest{InstanceId: id}); status.Code(err) != expected || strings.Contains(err.Error(), certificates.Directory) {
					t.Fatalf("instance selection: %v; want %v", err, expected)
				}
			}
			controls := instanceClient(t, server, certificates)
			if _, err := controls.StopInstance(ctx, &instancesv1.StopInstanceRequest{InstanceId: "edge-a"}); err != nil {
				t.Fatal(err)
			}
			if response, err := client.GetRuntimeInfo(ctx, &xrayv1.GetRuntimeInfoRequest{InstanceId: "edge-a"}); response != nil || status.Code(err) != codes.Unavailable {
				t.Fatalf("stopped API became available: %v, %v", response, err)
			}
			if err := xrayTraffic(ctx, secondAddress); err != nil {
				t.Fatalf("other instance affected: %v", err)
			}
			if _, err := controls.StartInstance(ctx, &instancesv1.StartInstanceRequest{InstanceId: "edge-a"}); err != nil {
				t.Fatal(err)
			}
			awaitRuntimeInfo(t, ctx, client, "edge-a")
			if err := xrayTraffic(ctx, firstAddress, secondAddress); err != nil {
				t.Fatal(err)
			}
			current, err := os.ReadFile(first.Xray.ConfigFile)
			if err != nil || !bytes.Equal(current, original) {
				t.Fatalf("native configuration modified: %v", err)
			}
		})
	}
}

func configureXrayAPI(t *testing.T, instance *settings.Instance, network string, services []string) {
	t.Helper()
	var address string
	if network == "unix" {
		// A short owned directory also respects Linux's Unix-address length bound.
		directory, err := os.MkdirTemp("", "xray-api-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.RemoveAll(directory); err != nil {
				t.Error(err)
			}
		})
		address = filepath.Join(directory, "api.sock")
	} else {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address = listener.Addr().String()
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
	}
	content, err := os.ReadFile(instance.Xray.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	var configuration map[string]json.RawMessage
	if err := json.Unmarshal(content, &configuration); err != nil {
		t.Fatal(err)
	}
	api, err := json.Marshal(struct {
		Tag      string   `json:"tag"`
		Listen   string   `json:"listen"`
		Services []string `json:"services"`
	}{"api", address, services})
	if err != nil {
		t.Fatal(err)
	}
	configuration["api"] = api
	configuration["stats"] = json.RawMessage("{}")
	content, err = json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(instance.Xray.ConfigFile, content, 0o600); err != nil {
		t.Fatal(err)
	}
	instance.Xray.API = &settings.XrayAPI{Network: network, Address: address, RequestTimeout: time.Second, MaxReceiveBytes: 4096, MaxConcurrent: 2}
}

// Only tests poll for observed API readiness; production reads never retry.
func awaitRuntimeInfo(t *testing.T, ctx context.Context, client xrayv1.XrayServiceClient, id string) *xrayv1.GetRuntimeInfoResponse {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		response, err := client.GetRuntimeInfo(ctx, &xrayv1.GetRuntimeInfoRequest{InstanceId: id})
		if err == nil {
			return response
		}
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("unexpected API readiness error: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Xray API did not become ready: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}
