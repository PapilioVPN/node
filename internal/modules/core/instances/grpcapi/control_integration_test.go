//go:build integration && linux

package grpcapi_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/modules/core/instances"
	instancesv1 "github.com/PapilioVPN/node/internal/modules/core/instances/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestControlPermissionsRemainIndependentOverMTLS(t *testing.T) {
	for _, test := range []struct {
		name          string
		read, control bool
	}{{name: "default deny"}, {name: "read only", read: true}, {name: "control only", control: true}} {
		t.Run(test.name, func(t *testing.T) {
			certificates := testutil.NewCertificates(t)
			cfg := certificates.Config()
			cfg.Communications.GRPC.Permissions = settings.GRPCPermissions{ReadInstanceStatus: test.read, ControlInstances: test.control}
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
			client := instancesv1.NewInstanceServiceClient(certificates.GRPCClient(t, server.Addr().String()))
			controlCode, readCode := codes.PermissionDenied, codes.PermissionDenied
			if test.control {
				controlCode = codes.NotFound
			}
			if test.read {
				readCode = codes.NotFound
			}
			if _, err := client.StartInstance(t.Context(), &instancesv1.StartInstanceRequest{InstanceId: "unknown"}); status.Code(err) != controlCode {
				t.Fatalf("start: %v; want %v", err, controlCode)
			}
			if _, err := client.StopInstance(t.Context(), &instancesv1.StopInstanceRequest{InstanceId: "unknown"}); status.Code(err) != controlCode {
				t.Fatalf("stop: %v; want %v", err, controlCode)
			}
			if _, err := client.GetInstanceStatus(t.Context(), &instancesv1.GetInstanceStatusRequest{InstanceId: "unknown"}); status.Code(err) != readCode {
				t.Fatalf("read: %v; want %v", err, readCode)
			}
			listCode := codes.PermissionDenied
			if test.read {
				listCode = codes.OK
			}
			page, err := client.ListInstanceStatuses(t.Context(), &instancesv1.ListInstanceStatusesRequest{PageSize: 1})
			if status.Code(err) != listCode || err == nil && (len(page.GetItems()) != 0 || page.NextAfterInstanceId != "") {
				t.Fatalf("list grant: %v, %v; want %v", page, err, listCode)
			}
			blankCode := codes.PermissionDenied
			if test.control {
				blankCode = codes.InvalidArgument
			}
			if _, err := client.StartInstance(t.Context(), &instancesv1.StartInstanceRequest{}); status.Code(err) != blankCode {
				t.Fatalf("blank identity bypassed authorization ordering: %v", err)
			}
			if !test.control {
				return
			}
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
					if _, err := instancesv1.NewInstanceServiceClient(connection).StartInstance(ctx, &instancesv1.StartInstanceRequest{InstanceId: "unknown"}); err == nil {
						t.Fatal("control grant bypassed mutual TLS")
					}
				})
			}
		})
	}
}

func TestControlRestartsOnlySelectedXrayAndRetainsShutdownOwnership(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	first, firstAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-a")
	second, secondAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-b")
	cfg := certificates.Config()
	cfg.Instances = []settings.Instance{first, second}
	cfg.Communications.GRPC.Permissions = settings.GRPCPermissions{ReadInstanceStatus: true, ControlInstances: true}
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
	})
	client := instancesv1.NewInstanceServiceClient(certificates.GRPCClient(t, server.Addr().String()))
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	if err := testutil.XrayTraffic(ctx, firstAddress, secondAddress); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		stopped, err := client.StopInstance(ctx, &instancesv1.StopInstanceRequest{InstanceId: "edge-a"})
		if err != nil || stopped.GetInstanceId() != "edge-a" || stopped.GetEngine() != "xray" || stopped.GetState() != instancesv1.InstanceState_INSTANCE_STATE_STOPPED || stopped.Failure != nil {
			t.Fatalf("selected stop: %v, %v", stopped, err)
		}
	}
	testutil.AssertTCPPortsReleased(t, firstAddress)
	other, err := client.GetInstanceStatus(ctx, &instancesv1.GetInstanceStatusRequest{InstanceId: "edge-b"})
	if err != nil || other.GetState() != instancesv1.InstanceState_INSTANCE_STATE_RUNNING || other.Failure != nil {
		t.Fatalf("unselected instance changed: %v, %v", other, err)
	}
	if err := testutil.XrayTraffic(ctx, secondAddress); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		started, err := client.StartInstance(ctx, &instancesv1.StartInstanceRequest{InstanceId: "edge-a"})
		if err != nil || started.GetInstanceId() != "edge-a" || started.GetState() != instancesv1.InstanceState_INSTANCE_STATE_RUNNING || started.Failure != nil {
			t.Fatalf("selected start: %v, %v", started, err)
		}
	}
	if err := testutil.XrayTraffic(ctx, firstAddress, secondAddress); err != nil {
		t.Fatal(err)
	}
	canceled, cancelStop := context.WithCancel(ctx)
	cancelStop()
	if _, err := client.StopInstance(canceled, &instancesv1.StopInstanceRequest{InstanceId: "edge-a"}); status.Code(err) != codes.Canceled {
		t.Fatalf("canceled request: %v", err)
	}
	if err := testutil.XrayTraffic(ctx, firstAddress, secondAddress); err != nil {
		t.Fatal(err)
	}
	if err := application.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	testutil.AssertTCPPortsReleased(t, firstAddress, secondAddress)
}

func TestControlFailureTransmitsPartialNativeOutcome(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	instance, address := testutil.ConfiguredXray(t, certificates.Directory, "failed")
	if err := os.WriteFile(instance.Xray.ConfigFile, []byte(`{"inbounds":`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := certificates.Config()
	cfg.Instances = []settings.Instance{instance}
	cfg.Communications.GRPC.Permissions = settings.GRPCPermissions{ReadInstanceStatus: true, ControlInstances: true}
	var server *rpc.Server
	var resources *instances.Resources
	application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&server, &resources))
	if err := application.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	// The native exit cause is expected and asserted, including at normal Stop.
	t.Cleanup(func() {
		_ = application.Stop(context.Background())
		_ = server.Close()
		_ = resources.Close()
	})
	client := instancesv1.NewInstanceServiceClient(certificates.GRPCClient(t, server.Addr().String()))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		observed, err := client.GetInstanceStatus(ctx, &instancesv1.GetInstanceStatusRequest{InstanceId: "failed"})
		if err != nil {
			t.Fatal(err)
		}
		if observed.GetState() == instancesv1.InstanceState_INSTANCE_STATE_EXITED {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("native exit was not observed")
		case <-ticker.C:
		}
	}
	response, err := client.StopInstance(ctx, &instancesv1.StopInstanceRequest{InstanceId: "failed"})
	failure := status.Convert(err)
	if response != nil || failure.Code() != codes.Internal || strings.Contains(err.Error(), certificates.Directory) {
		t.Fatalf("failed stop: %v, %v", response, err)
	}
	details := failure.Details()
	if len(details) != 1 {
		t.Fatalf("partial observation missing from native gRPC trailers: %v", details)
	}
	outcome, ok := details[0].(*instancesv1.InstanceStatus)
	if !ok || outcome.InstanceId != "failed" || outcome.Engine != "xray" || outcome.State != instancesv1.InstanceState_INSTANCE_STATE_EXITED || outcome.Failure == nil || outcome.Failure.ExitCode == nil || outcome.Failure.GetExitCode() != 23 || strings.Contains(outcome.String(), certificates.Directory) {
		t.Fatalf("native outcome lost or exposed private data: %v", details)
	}
	var nativeExit *exec.ExitError
	if err := application.Stop(ctx); !errors.As(err, &nativeExit) || nativeExit.ExitCode() != 23 {
		t.Fatalf("native owner lost the exit cause: %v", err)
	}
	testutil.AssertTCPPortsReleased(t, address)
}
