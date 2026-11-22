package grpcapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	trafficv1 "github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGRPCExactSelectionAndFailurePrivacy(t *testing.T) {
	reader := &readerFixture{}
	service, _ := services.New([]services.Binding{{ID: "a", Reader: reader}, {ID: "disabled"}}, statusFixture{})
	h := Handler{Service: service, Timeout: time.Second}
	valid := func() *trafficv1.GetTrafficCounterRequest {
		return &trafficv1.GetTrafficCounterRequest{InstanceId: "a", Scope: trafficv1.Scope_SCOPE_OUTBOUND, Name: " A>>>B ", Direction: trafficv1.Direction_DIRECTION_DOWNLINK}
	}
	got, err := h.GetTrafficCounter(context.Background(), valid())
	if err != nil || got.Bytes != -9007199254740993 || got.Source != domain.Source || got.Generation != 1 || reader.calls != 1 || reader.selection.Scope != domain.Outbound || reader.selection.Direction != domain.Downlink || reader.selection.Name != " A>>>B " {
		t.Fatal(got, err, reader)
	}
	for _, request := range []*trafficv1.GetTrafficCounterRequest{nil, {}, {InstanceId: "a", Scope: 257, Name: "a", Direction: 1}, {InstanceId: "a", Scope: 1, Name: "a", Direction: 257}, {InstanceId: "a", Scope: 1, Name: "", Direction: 1}} {
		if _, err := h.GetTrafficCounter(context.Background(), request); status.Code(err) != codes.InvalidArgument {
			t.Fatal("bad shape accepted", err)
		}
	}
	if reader.calls != 1 {
		t.Fatal("invalid selection dispatched")
	}
	for _, test := range []struct {
		cause error
		code  codes.Code
	}{{domain.ErrCounterNotFound, codes.NotFound}, {domain.ErrCapacity, codes.ResourceExhausted}, {domain.ErrUnsupported, codes.FailedPrecondition}, {domain.ErrExecutionChanged, codes.Aborted}, {errors.Join(domain.ErrExecutionChanged, domain.ErrCounterNotFound), codes.Aborted}, {context.Canceled, codes.Canceled}, {context.DeadlineExceeded, codes.DeadlineExceeded}, {errors.New("private endpoint"), codes.Unavailable}} {
		reader.err = test.cause
		result, err := h.GetTrafficCounter(context.Background(), valid())
		if result != nil || status.Code(err) != test.code || strings.Contains(err.Error(), "private") {
			t.Fatal(result, err)
		}
	}
}
