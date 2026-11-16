//go:build integration && linux

package bootstrap_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/modules/core/instances"
	nodev1 "github.com/PapilioVPN/node/internal/modules/core/instances/grpcapi/proto/v1"
	systemv1 "github.com/PapilioVPN/node/internal/modules/system/info/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

func TestInstanceStatusDeniedByDefault(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	var server *rpc.Server
	application := bootstrap.New(certificates.Config(), "test", fx.NopLogger, fx.Populate(&server))
	if err := application.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = application.Stop(context.Background())
		_ = server.Close()
	})
	connection, err := grpc.NewClient(server.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(certificates.ClientTLS(t))))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_, err = nodev1.NewInstanceServiceClient(connection).GetInstanceStatus(t.Context(), &nodev1.GetInstanceStatusRequest{InstanceId: "edge"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("trusted client must need an explicit status grant: %v", err)
	}
}

func TestInstanceStatusReportsRealExitWithoutAffectingOtherInstance(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	first, firstAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-a")
	second, secondAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-b")
	if err := os.WriteFile(second.Xray.ConfigFile, []byte(`{"inbounds":`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := certificates.Config()
	cfg.Instances = []settings.Instance{first, second}
	cfg.Communications.GRPC.Permissions.ReadInstanceStatus = true
	var server *rpc.Server
	var resources *instances.Resources
	application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&server, &resources))
	if err := application.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	// The failed Xray's native exit is expected and asserted below; always join all resources.
	t.Cleanup(func() {
		_ = application.Stop(context.Background())
		_ = server.Close()
		_ = resources.Close()
	})
	connection, err := grpc.NewClient(server.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(certificates.ClientTLS(t))))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	client := nodev1.NewInstanceServiceClient(connection)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var exited *nodev1.GetInstanceStatusResponse
	for {
		exited, err = client.GetInstanceStatus(ctx, &nodev1.GetInstanceStatusRequest{InstanceId: "edge-b"})
		if err != nil {
			t.Fatal(err)
		}
		if exited.GetState() == nodev1.InstanceState_INSTANCE_STATE_EXITED {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("Xray exit was not observed through gRPC")
		case <-ticker.C:
		}
	}
	if exited.InstanceId != "edge-b" || exited.Engine != "xray" || exited.Failure == nil ||
		exited.Failure.GetCode() != nodev1.InstanceFailureCode_INSTANCE_FAILURE_CODE_EXECUTION_FAILED ||
		exited.Failure.ExitCode == nil || exited.Failure.GetExitCode() != 23 {
		t.Fatalf("native failure was not represented: %v", exited)
	}
	if strings.Contains(exited.String(), certificates.Directory) {
		t.Fatal("native error path leaked through gRPC")
	}
	other, err := client.GetInstanceStatus(ctx, &nodev1.GetInstanceStatusRequest{InstanceId: "edge-a"})
	if err != nil || other.GetState() != nodev1.InstanceState_INSTANCE_STATE_RUNNING || other.Failure != nil {
		t.Fatalf("other instance was affected: %v, %v", other, err)
	}
	page, err := client.ListInstanceStatuses(ctx, &nodev1.ListInstanceStatusesRequest{PageSize: 2})
	if err != nil || len(page.GetItems()) != 2 || page.Items[0].InstanceId != "edge-a" || page.Items[0].State != nodev1.InstanceState_INSTANCE_STATE_RUNNING || page.Items[1].InstanceId != "edge-b" || page.Items[1].State != nodev1.InstanceState_INSTANCE_STATE_EXITED || page.Items[1].Failure == nil || page.Items[1].Failure.ExitCode == nil || page.Items[1].Failure.GetExitCode() != 23 || page.NextAfterInstanceId != "" || strings.Contains(page.String(), certificates.Directory) {
		t.Fatalf("failed execution was not a readable collection item: %v, %v", page, err)
	}
	if err := testutil.XrayTraffic(ctx, firstAddress); err != nil {
		t.Fatal(err)
	}
	testutil.AssertTCPPortsReleased(t, secondAddress)
	for id, code := range map[string]codes.Code{"missing": codes.NotFound, "": codes.InvalidArgument} {
		if _, err := client.GetInstanceStatus(ctx, &nodev1.GetInstanceStatusRequest{InstanceId: id}); status.Code(err) != code {
			t.Fatalf("identity %q mapping = %v", id, err)
		}
	}
	canceled, cancelRead := context.WithCancel(ctx)
	cancelRead()
	if _, err := client.GetInstanceStatus(canceled, &nodev1.GetInstanceStatusRequest{InstanceId: "edge-a"}); status.Code(err) != codes.Canceled {
		t.Fatalf("client cancellation lost: %v", err)
	}
	info, err := systemv1.NewNodeServiceClient(connection).GetNodeInfo(ctx, &systemv1.GetNodeInfoRequest{})
	if err != nil || !slices.Contains(info.GetCapabilities(), "instance.status.read") {
		t.Fatalf("implemented observation not advertised: %v, %v", info, err)
	}
	probe, err := healthv1.NewHealthClient(connection).Check(ctx, &healthv1.HealthCheckRequest{Service: nodev1.InstanceService_ServiceDesc.ServiceName})
	if err != nil || probe.GetStatus() != healthv1.HealthCheckResponse_SERVING {
		t.Fatalf("management service health was confused with engine health: %v, %v", probe, err)
	}
	var nativeExit *exec.ExitError
	if err := application.Stop(ctx); !errors.As(err, &nativeExit) || nativeExit.ExitCode() != 23 {
		t.Fatalf("native cleanup cause lost: %v", err)
	}
	testutil.AssertTCPPortsReleased(t, firstAddress)
}

func TestStatusGrantDoesNotBypassMutualTLS(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	cfg := certificates.Config()
	cfg.Communications.GRPC.Permissions.ReadInstanceStatus = true
	var server *rpc.Server
	application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&server))
	if err := application.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = application.Stop(context.Background())
		_ = server.Close()
	})
	for _, kind := range []string{"missing", "untrusted"} {
		t.Run(kind, func(t *testing.T) {
			identity := certificates.ClientTLS(t)
			if kind == "missing" {
				identity.Certificates = nil
			} else {
				identity.Certificates = testutil.NewCertificates(t).ClientTLS(t).Certificates
			}
			connection, err := grpc.NewClient(server.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(identity)))
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if _, err := nodev1.NewInstanceServiceClient(connection).GetInstanceStatus(ctx, &nodev1.GetInstanceStatusRequest{InstanceId: "edge"}); err == nil {
				t.Fatal("status grant accepted an unauthenticated client")
			}
		})
	}
}
