package grpcapi

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/domain"
	healthv1 "github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestWirePresenceExactWidthsAndSanitizedErrors(t *testing.T) {
	r := &reader{}
	s, err := services.New([]services.Binding{{ID: "edge", Reader: r}, {ID: "disabled"}}, running{})
	if err != nil {
		t.Fatal(err)
	}
	h := Handler{Service: s, Timeout: time.Second}
	ctx := context.Background()
	if _, err := h.GetOutboundStatus(ctx, &healthv1.GetOutboundStatusRequest{InstanceId: "edge"}); status.Code(err) != codes.InvalidArgument || r.calls != 0 {
		t.Fatal(err)
	}
	tag := ""
	got, err := h.GetOutboundStatus(ctx, &healthv1.GetOutboundStatusRequest{InstanceId: "edge", OutboundTag: &tag})
	if err != nil || got.Status == nil || got.Status.DelayMs != math.MinInt64 || got.Status.HealthPing.All != math.MaxInt64 || got.Observation.Generation != math.MaxUint64 || got.Status.LatencyMs != nil || got.Status.SampledAt != nil {
		t.Fatal(got, err)
	}
	encoded, err := proto.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	decoded := &healthv1.OutboundObservation{}
	if err := proto.Unmarshal(encoded, decoded); err != nil || !proto.Equal(got, decoded) {
		t.Fatal(decoded, err)
	}
	tag = "missing"
	missing, err := h.GetOutboundStatus(ctx, &healthv1.GetOutboundStatusRequest{InstanceId: "edge", OutboundTag: &tag})
	if err != nil || missing.Status != nil || missing.Observation == nil {
		t.Fatal(missing, err)
	}
	for _, tt := range []struct {
		err     error
		code    codes.Code
		message string
	}{
		{domain.ErrDisabled, codes.FailedPrecondition, "api_not_configured"}, {domain.ErrUnsupported, codes.FailedPrecondition, "unsupported"},
		{domain.ErrCapacity, codes.ResourceExhausted, "resource_exhausted"}, {domain.ErrExecutionChanged, codes.Aborted, "execution_changed"},
		{context.Canceled, codes.Canceled, "canceled"}, {context.DeadlineExceeded, codes.DeadlineExceeded, "deadline_exceeded"},
		{errors.New("private target or credential"), codes.Unavailable, "unavailable"},
	} {
		r.err = errors.Join(tt.err, errors.New("private target or credential"))
		got, err := h.GetOutboundStatus(ctx, &healthv1.GetOutboundStatusRequest{InstanceId: "edge", OutboundTag: &tag})
		if got != nil || status.Code(err) != tt.code || status.Convert(err).Message() != tt.message {
			t.Fatal(got, err)
		}
	}
}
