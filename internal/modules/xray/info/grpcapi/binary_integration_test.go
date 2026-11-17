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
	"github.com/PapilioVPN/node/internal/testutil"
)

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
	client := xrayv1.NewXrayServiceClient(certificates.GRPCClient(t, server.Addr().String()))

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
	controls := instancesv1.NewInstanceServiceClient(certificates.GRPCClient(t, server.Addr().String()))
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
	client := xrayv1.NewXrayServiceClient(certificates.GRPCClient(t, server.Addr().String()))
	for _, id := range []string{"unknown", ""} {
		if _, err := client.GetBinaryInfo(t.Context(), &infov1.GetBinaryInfoRequest{InstanceId: id}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("other instance grants must not authorize engine information: %v", err)
		}
		if _, err := client.GetRuntimeInfo(t.Context(), &infov1.GetRuntimeInfoRequest{InstanceId: id}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("other instance grants must not authorize runtime information: %v", err)
		}
	}
}
