//go:build integration && linux

package grpcapi_test

import (
	"context"
	"math"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/modules/core/instances"
	instancesv1 "github.com/PapilioVPN/node/internal/modules/core/instances/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestStatusPagesDiscoverSortedInstancesAndObserveCurrentState(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	first, firstAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-b")
	second, secondAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-a")
	cfg := certificates.Config()
	// Startup order is deliberately different from list order.
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
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := testutil.XrayTraffic(ctx, firstAddress, secondAddress); err != nil {
		t.Fatal(err)
	}
	firstPage, err := client.ListInstanceStatuses(ctx, &instancesv1.ListInstanceStatusesRequest{PageSize: 1})
	if err != nil || len(firstPage.GetItems()) != 1 || firstPage.Items[0].InstanceId != "edge-a" || firstPage.Items[0].State != instancesv1.InstanceState_INSTANCE_STATE_RUNNING || firstPage.NextAfterInstanceId != "edge-a" {
		t.Fatalf("first page: %v, %v", firstPage, err)
	}
	if _, err := client.StopInstance(ctx, &instancesv1.StopInstanceRequest{InstanceId: "edge-b"}); err != nil {
		t.Fatal(err)
	}
	next, err := client.ListInstanceStatuses(ctx, &instancesv1.ListInstanceStatusesRequest{PageSize: 1, AfterInstanceId: firstPage.NextAfterInstanceId})
	if err != nil || len(next.GetItems()) != 1 || next.Items[0].InstanceId != "edge-b" || next.Items[0].State != instancesv1.InstanceState_INSTANCE_STATE_STOPPED || next.Items[0].Failure != nil || next.NextAfterInstanceId != "" {
		t.Fatalf("second page lost current state: %v, %v", next, err)
	}
	all, err := client.ListInstanceStatuses(ctx, &instancesv1.ListInstanceStatusesRequest{PageSize: math.MaxInt32})
	if err != nil || len(all.GetItems()) != 2 || all.NextAfterInstanceId != "" {
		t.Fatalf("large size was not bounded by configured membership: %v, %v", all, err)
	}
	empty, err := client.ListInstanceStatuses(ctx, &instancesv1.ListInstanceStatusesRequest{PageSize: 1, AfterInstanceId: "edge-z"})
	if err != nil || len(empty.GetItems()) != 0 || empty.NextAfterInstanceId != "" {
		t.Fatalf("end of collection: %v, %v", empty, err)
	}
	for _, size := range []int32{0, -1} {
		if _, err := client.ListInstanceStatuses(ctx, &instancesv1.ListInstanceStatusesRequest{PageSize: size}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid size: %v", err)
		}
	}
	canceled, cancelRead := context.WithCancel(ctx)
	cancelRead()
	if _, err := client.ListInstanceStatuses(canceled, &instancesv1.ListInstanceStatusesRequest{PageSize: 1}); status.Code(err) != codes.Canceled {
		t.Fatalf("read cancellation: %v", err)
	}
	if err := testutil.XrayTraffic(ctx, secondAddress); err != nil {
		t.Fatal(err)
	}
	testutil.AssertTCPPortsReleased(t, firstAddress)
}
