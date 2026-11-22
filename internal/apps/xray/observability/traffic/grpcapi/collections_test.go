package grpcapi

import (
	"context"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/app/services"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestResetRequiresExplicitRevisionAndPreservesEvidence(t *testing.T) {
	reader := &readerFixture{}
	service, _ := services.New([]services.Binding{{ID: "a", Reader: reader, Resetter: reader}}, statusFixture{})
	handler := Handler{Service: service, Timeout: time.Second}
	initial, err := handler.GetTrafficResetStatus(context.Background(), &v1.GetTrafficResetStatusRequest{InstanceId: "a"})
	if err != nil {
		t.Fatal(err)
	}
	request := &v1.ResetTrafficRequest{InstanceId: "a", Epoch: initial.Epoch, OperationId: "op", Scope: v1.Scope_SCOPE_USER, Name: "A>>>B", Direction: v1.Direction_DIRECTION_UPLINK}
	if _, err := handler.ResetTrafficCounter(context.Background(), request); status.Code(err) != codes.InvalidArgument || reader.calls != 0 {
		t.Fatal(err, reader.calls)
	}
	revision := uint64(0)
	request.ExpectedRevision = &revision
	report, err := handler.ResetTrafficCounter(context.Background(), request)
	if err != nil || report.Outcome != "completed" || report.Items[0].PreviousBytes == nil || *report.Items[0].PreviousBytes != -9007199254740993 {
		t.Fatal(report, err)
	}
	if _, err := handler.ResetTrafficCounter(context.Background(), request); err != nil || reader.calls != 1 {
		t.Fatal("replay", err, reader.calls)
	}
	request.OperationId = "stale"
	if _, err := handler.ResetTrafficCounter(context.Background(), request); status.Code(err) != codes.FailedPrecondition || reader.calls != 1 {
		t.Fatal(err, reader.calls)
	}
	for _, r := range []*v1.ResetTrafficRequest{nil, {InstanceId: "a", ExpectedRevision: &revision, Scope: v1.Scope(257), Epoch: initial.Epoch, OperationId: "bad"}} {
		if _, err := handler.ResetTrafficCounter(context.Background(), r); status.Code(err) != codes.InvalidArgument {
			t.Fatal(err)
		}
	}
	if _, err := handler.GetTrafficPair(context.Background(), nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := handler.ListTrafficCounters(context.Background(), &v1.ListTrafficCountersRequest{InstanceId: "a", Scope: v1.Scope_SCOPE_USER, PageSize: 51}); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
}
