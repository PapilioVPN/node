package grpcapi_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/core/traffic/domain"
	trafficgrpc "github.com/PapilioVPN/node/internal/modules/core/traffic/grpcapi"
	trafficv1 "github.com/PapilioVPN/node/internal/modules/core/traffic/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi"
)

type trafficResetter func(context.Context, instances.InstanceID, domain.TrafficSelection) (domain.InstanceTrafficCounterReset, error)

func (f trafficResetter) ResetTrafficCounter(ctx context.Context, id instances.InstanceID, selection domain.TrafficSelection) (domain.InstanceTrafficCounterReset, error) {
	return f(ctx, id, selection)
}

func resetRequest() *trafficv1.ResetTrafficCounterRequest {
	return &trafficv1.ResetTrafficCounterRequest{InstanceId: "edge", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_USER, Name: "user", Direction: trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK}
}

func TestResetRequiresIdentityAndItsIndependentGrantBeforeShape(t *testing.T) {
	if _, err := (trafficgrpc.TrafficAPI{AllowStatsReset: true}).ResetTrafficCounter(t.Context(), nil); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("reset without identity: %v", err)
	}
	if _, err := (grpcapi.Handler{AllowInfoRead: true, AllowOnlineRead: true, TrafficAPI: trafficgrpc.TrafficAPI{AllowStatsRead: true}}).ResetTrafficCounter(verifiedContext(t), nil); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("reads granted reset: %v", err)
	}
	h := trafficgrpc.TrafficAPI{AllowStatsReset: true}
	for _, request := range []*trafficv1.ResetTrafficCounterRequest{nil, {},
		{InstanceId: "edge", Name: "user", Scope: 999, Direction: trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK},
		{InstanceId: "edge", Name: "user", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_USER, Direction: 999},
		{InstanceId: "edge", Name: " ", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_USER, Direction: trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK},
	} {
		if _, err := h.ResetTrafficCounter(verifiedContext(t), request); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid shape reached resetter: %v", err)
		}
	}
}

func TestResetWirePreservesPriorBytesAndAcknowledgedEffect(t *testing.T) {
	for _, value := range []int64{0, math.MinInt64, math.MaxInt64} {
		ctx, cancel := context.WithCancel(verifiedContext(t))
		acknowledged := time.UnixMilli(1700000000123)
		request := resetRequest()
		request.InstanceId = " edge "
		request.Name = " exact>>>user "
		calls := 0
		h := trafficgrpc.TrafficAPI{AllowStatsReset: true, Resets: trafficResetter(func(received context.Context, id instances.InstanceID, selection domain.TrafficSelection) (domain.InstanceTrafficCounterReset, error) {
			calls++
			if received != ctx || id != " edge " || selection.Name != request.Name || selection.Scope != domain.UserTraffic || selection.Direction != domain.Uplink {
				t.Fatal("reset selection/context changed")
			}
			cancel()
			return domain.InstanceTrafficCounterReset{InstanceID: id, Selection: selection, Reset: domain.TrafficCounterReset{PreviousBytes: value, AcknowledgedAt: acknowledged}}, nil
		})}
		response, err := h.ResetTrafficCounter(ctx, request)
		cancel()
		if err != nil || response.InstanceId != request.InstanceId || response.Name != request.Name || response.Scope != request.Scope || response.Direction != request.Direction || response.PreviousBytes != value || response.AcknowledgedAtUnixMs != acknowledged.UnixMilli() || calls != 1 {
			t.Fatalf("acknowledgement changed: %v %v", response, err)
		}
	}
}

func TestResetErrorsReportEffectUncertaintyWithoutPrivateDiagnostics(t *testing.T) {
	for _, entry := range []struct {
		cause   error
		code    codes.Code
		unknown bool
	}{
		{instances.ErrNotFound, codes.NotFound, false}, {domain.ErrAPIDisabled, codes.FailedPrecondition, false},
		{domain.ErrAPIBusy, codes.ResourceExhausted, false}, {domain.ErrTrafficCounterNotFound, codes.NotFound, false},
		{domain.ErrTrafficUnsupported, codes.FailedPrecondition, false}, {context.Canceled, codes.Canceled, false},
		{errors.Join(domain.ErrTrafficResetUnknown, context.DeadlineExceeded), codes.DeadlineExceeded, true},
		{errors.Join(domain.ErrTrafficResetUnknown, domain.ErrAPIResourceLimit), codes.ResourceExhausted, true},
		{errors.Join(domain.ErrTrafficResetUnknown, domain.ErrInvalidTrafficResponse), codes.Unavailable, true},
	} {
		h := trafficgrpc.TrafficAPI{AllowStatsReset: true, Resets: trafficResetter(func(context.Context, instances.InstanceID, domain.TrafficSelection) (domain.InstanceTrafficCounterReset, error) {
			return domain.InstanceTrafficCounterReset{Reset: domain.TrafficCounterReset{PreviousBytes: 99}}, errors.Join(errors.New("private-endpoint-diagnostics"), entry.cause)
		})}
		response, err := h.ResetTrafficCounter(verifiedContext(t), resetRequest())
		if response != nil || status.Code(err) != entry.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("reset failure mapping: %v %v", response, err)
		}
		details := status.Convert(err).Details()
		if len(details) != 1 {
			t.Fatalf("effect outcome missing: %v", err)
		}
		failure, ok := details[0].(*trafficv1.TrafficCounterResetFailure)
		want := trafficv1.TrafficCounterResetOutcome_TRAFFIC_COUNTER_RESET_OUTCOME_NOT_APPLIED
		if entry.unknown {
			want = trafficv1.TrafficCounterResetOutcome_TRAFFIC_COUNTER_RESET_OUTCOME_UNKNOWN
		}
		if !ok || failure.Outcome != want {
			t.Fatalf("effect uncertainty changed: %v", details)
		}
	}
}
