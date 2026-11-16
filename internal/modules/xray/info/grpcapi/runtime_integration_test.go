//go:build integration && linux

package grpcapi_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/bootstrap"
	xrayv1 "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
	"github.com/PapilioVPN/node/internal/modules/core/instances"
	instancesv1 "github.com/PapilioVPN/node/internal/modules/core/instances/grpcapi/proto/v1"
	infov1 "github.com/PapilioVPN/node/internal/modules/xray/info/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

func TestXrayRuntimeInfoOverMTLSAndNativeReconnect(t *testing.T) {
	for _, network := range []string{"unix", "tcp"} {
		t.Run(network, func(t *testing.T) {
			certificates := testutil.NewCertificates(t)
			first, firstAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-a")
			second, secondAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-b")
			testutil.ConfigureXrayAPI(t, &first, network, []string{"StatsService"})
			// A real API without StatsService must not become successful empty facts.
			testutil.ConfigureXrayAPI(t, &second, network, []string{})
			original, err := os.ReadFile(first.Xray.ConfigFile)
			if err != nil {
				t.Fatal(err)
			}
			cfg := certificates.Config()
			cfg.Instances = []settings.Instance{first, second}
			cfg.Communications.GRPC.Permissions.ReadEngineInfo = true
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
				testutil.AssertTCPPortsReleased(t, firstAddress, secondAddress)
				if network == "tcp" {
					testutil.AssertTCPPortsReleased(t, first.Xray.API.Address, second.Xray.API.Address)
				}
			})
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if err := testutil.XrayTraffic(ctx, firstAddress, secondAddress); err != nil {
				t.Fatal(err)
			}
			client := xrayv1.NewXrayServiceClient(certificates.GRPCClient(t, server.Addr().String()))
			response := awaitRuntimeInfo(t, ctx, client, "edge-a")

			info := response.GetRuntime()
			if response.InstanceId != "edge-a" || info.GetGoroutines() == 0 || info.AllocatedBytes == 0 || info.SysBytes < info.AllocatedBytes || info.Mallocs < info.Frees || info.LiveObjects != info.Mallocs-info.Frees || info.ObservedAtUnixMs <= 0 {
				t.Fatalf("native runtime facts: %v", response)
			}
			for id, expected := range map[string]codes.Code{"missing": codes.NotFound, " edge-a ": codes.NotFound, "": codes.InvalidArgument, "edge-b": codes.FailedPrecondition} {
				if _, err := client.GetRuntimeInfo(ctx, &infov1.GetRuntimeInfoRequest{InstanceId: id}); status.Code(err) != expected || strings.Contains(err.Error(), certificates.Directory) {
					t.Fatalf("instance selection: %v; want %v", err, expected)
				}
			}
			controls := instancesv1.NewInstanceServiceClient(certificates.GRPCClient(t, server.Addr().String()))
			if _, err := controls.StopInstance(ctx, &instancesv1.StopInstanceRequest{InstanceId: "edge-a"}); err != nil {
				t.Fatal(err)
			}
			if response, err := client.GetRuntimeInfo(ctx, &infov1.GetRuntimeInfoRequest{InstanceId: "edge-a"}); response != nil || status.Code(err) != codes.Unavailable {
				t.Fatalf("stopped API became available: %v, %v", response, err)
			}
			if err := testutil.XrayTraffic(ctx, secondAddress); err != nil {
				t.Fatalf("other instance affected: %v", err)
			}
			if _, err := controls.StartInstance(ctx, &instancesv1.StartInstanceRequest{InstanceId: "edge-a"}); err != nil {
				t.Fatal(err)
			}
			awaitRuntimeInfo(t, ctx, client, "edge-a")
			if err := testutil.XrayTraffic(ctx, firstAddress, secondAddress); err != nil {
				t.Fatal(err)
			}
			current, err := os.ReadFile(first.Xray.ConfigFile)
			if err != nil || !bytes.Equal(current, original) {
				t.Fatalf("native configuration modified: %v", err)
			}
		})
	}
}

// Only tests poll for observed API readiness; production reads never retry.
func awaitRuntimeInfo(t *testing.T, ctx context.Context, client xrayv1.XrayServiceClient, id string) *infov1.GetRuntimeInfoResponse {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		response, err := client.GetRuntimeInfo(ctx, &infov1.GetRuntimeInfoRequest{InstanceId: id})
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
