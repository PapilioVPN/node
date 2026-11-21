package grpcapi

import (
	"context"
	"strings"
	"testing"

	instancesv1 "github.com/PapilioVPN/node/internal/apps/core/engines/instances/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGRPCPrecisionRejectionAndPartialFailureDetails(t *testing.T) {
	h, r := fixture()
	result, err := h.GetInstanceStatus(context.Background(), &instancesv1.GetInstanceStatusRequest{InstanceId: "a"})
	if err != nil || result.Generation != 9007199254740993 || result.ExitCode != nil {
		t.Fatal(result, err)
	}
	_, err = h.StartInstance(context.Background(), &instancesv1.StartInstanceRequest{InstanceId: "missing"})
	if status.Code(err) != codes.NotFound || r.calls != 0 {
		t.Fatal(err)
	}
	r.fail = true
	_, err = h.RestartInstance(context.Background(), &instancesv1.RestartInstanceRequest{InstanceId: "a"})
	if status.Code(err) != codes.Unavailable || strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
	details := status.Convert(err).Details()
	if len(details) != 1 {
		t.Fatal("missing failure progress")
	}
	detail, ok := details[0].(*instancesv1.ControlResult)
	if !ok || detail.Effect != "partial" || detail.Status.State != "stopped" {
		t.Fatal(details)
	}
}
