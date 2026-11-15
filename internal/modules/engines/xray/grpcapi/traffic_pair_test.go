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
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
)

type pairReader func(context.Context, instances.InstanceID, domain.TrafficPairSelection) (domain.InstanceTrafficPair, error)

func (f pairReader) GetTrafficPair(ctx context.Context, id instances.InstanceID, selection domain.TrafficPairSelection) (domain.InstanceTrafficPair, error) {
	return f(ctx, id, selection)
}

type pairResetter func(context.Context, instances.InstanceID, domain.TrafficPairSelection) (domain.InstanceTrafficPairReset, error)

func (f pairResetter) ResetTrafficPair(ctx context.Context, id instances.InstanceID, selection domain.TrafficPairSelection) (domain.InstanceTrafficPairReset, error) {
	return f(ctx, id, selection)
}

func TestPairRPCsCheckIdentityIndependentGrantsAndExactShapeBeforeLookup(t *testing.T) {
	h := grpcapi.Handler{AllowStatsRead: true, AllowStatsReset: true}
	if _, err := h.GetTrafficPair(t.Context(), nil); status.Code(err) != codes.Unauthenticated {
		t.Fatal(err)
	}
	if _, err := h.ResetTrafficPair(t.Context(), nil); status.Code(err) != codes.Unauthenticated {
		t.Fatal(err)
	}
	if _, err := (grpcapi.Handler{AllowStatsReset: true}).GetTrafficPair(verifiedContext(t), nil); status.Code(err) != codes.PermissionDenied {
		t.Fatal(err)
	}
	if _, err := (grpcapi.Handler{AllowStatsRead: true}).ResetTrafficPair(verifiedContext(t), nil); status.Code(err) != codes.PermissionDenied {
		t.Fatal(err)
	}
	for _, request := range []*xrayv1.GetTrafficPairRequest{nil, {}, {InstanceId: "edge", Scope: 999, Name: "user"}, {InstanceId: "edge", Scope: xrayv1.TrafficScope_TRAFFIC_SCOPE_USER, Name: " "}} {
		if _, err := h.GetTrafficPair(verifiedContext(t), request); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("read shape: %v", err)
		}
	}
	for _, request := range []*xrayv1.ResetTrafficPairRequest{nil, {}, {InstanceId: "edge", Scope: 999, Name: "user"}, {InstanceId: "edge", Scope: xrayv1.TrafficScope_TRAFFIC_SCOPE_USER, Name: " "}} {
		if _, err := h.ResetTrafficPair(verifiedContext(t), request); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("reset shape: %v", err)
		}
	}
}

func TestPairReadWirePreservesAbsenceZeroSignedValuesAndIndependentTimes(t *testing.T) {
	for _, present := range []int{0, 1, 2, 3} {
		ctx := verifiedContext(t)
		h := grpcapi.Handler{AllowStatsRead: true, Pairs: pairReader(func(received context.Context, id instances.InstanceID, selection domain.TrafficPairSelection) (domain.InstanceTrafficPair, error) {
			if received != ctx || id != " edge " || selection != (domain.TrafficPairSelection{Scope: domain.UserTraffic, Name: " exact>>>opaque "}) {
				t.Fatal("read input changed")
			}
			pair := domain.TrafficPair{}
			if present&1 != 0 {
				pair.Uplink = &domain.TrafficCounter{Bytes: 0, ObservedAt: time.UnixMilli(100)}
			}
			if present&2 != 0 {
				pair.Downlink = &domain.TrafficCounter{Bytes: math.MinInt64, ObservedAt: time.UnixMilli(200)}
			}
			return domain.InstanceTrafficPair{InstanceID: id, Selection: selection, Pair: pair}, nil
		})}
		response, err := h.GetTrafficPair(ctx, &xrayv1.GetTrafficPairRequest{InstanceId: " edge ", Scope: xrayv1.TrafficScope_TRAFFIC_SCOPE_USER, Name: " exact>>>opaque "})
		if err != nil || response.InstanceId != " edge " || response.Name != " exact>>>opaque " || (response.Uplink != nil) != (present&1 != 0) || (response.Downlink != nil) != (present&2 != 0) || present&1 != 0 && (response.Uplink.Bytes != 0 || response.Uplink.ObservedAtUnixMs != 100) || present&2 != 0 && (response.Downlink.Bytes != math.MinInt64 || response.Downlink.ObservedAtUnixMs != 200) {
			t.Fatalf("pair read: %v %v", response, err)
		}
	}
	for cause, code := range map[error]codes.Code{instances.ErrNotFound: codes.NotFound, domain.ErrAPIDisabled: codes.FailedPrecondition, domain.ErrTrafficUnsupported: codes.FailedPrecondition, domain.ErrAPIBusy: codes.ResourceExhausted, domain.ErrInvalidTrafficResponse: codes.Unavailable, context.Canceled: codes.Canceled} {
		h := grpcapi.Handler{AllowStatsRead: true, Pairs: pairReader(func(context.Context, instances.InstanceID, domain.TrafficPairSelection) (domain.InstanceTrafficPair, error) {
			return domain.InstanceTrafficPair{Pair: domain.TrafficPair{Uplink: &domain.TrafficCounter{Bytes: 99}}}, errors.Join(cause, errors.New("private endpoint"))
		})}
		result, err := h.GetTrafficPair(verifiedContext(t), &xrayv1.GetTrafficPairRequest{InstanceId: "edge", Scope: xrayv1.TrafficScope_TRAFFIC_SCOPE_USER, Name: "user"})
		if result != nil || status.Code(err) != code || strings.Contains(err.Error(), "private") {
			t.Fatalf("pair read failure: %v %v", result, err)
		}
	}
}

func TestPairResetWireRetainsMissingAndUncertainProgressIncludingLateAcknowledgement(t *testing.T) {
	for _, state := range []domain.TrafficResetState{domain.TrafficResetAcknowledged, domain.TrafficResetMissing, domain.TrafficResetUnknown, domain.TrafficResetNotAttempted} {
		ctx, cancel := context.WithCancel(verifiedContext(t))
		defer cancel()
		h := grpcapi.Handler{AllowStatsReset: true, PairResets: pairResetter(func(received context.Context, id instances.InstanceID, selection domain.TrafficPairSelection) (domain.InstanceTrafficPairReset, error) {
			if received != ctx || id != " edge " || selection.Name != " exact>>>opaque " || selection.Scope != domain.OutboundTraffic {
				t.Fatal("reset input changed")
			}
			report := domain.InstanceTrafficPairReset{InstanceID: id, Selection: selection, Items: [2]domain.TrafficCounterResetEntry{{Name: selection.Name, Direction: domain.Uplink, State: domain.TrafficResetAcknowledged, Reset: &domain.TrafficCounterReset{PreviousBytes: math.MaxInt64, AcknowledgedAt: time.UnixMilli(100)}}, {Name: selection.Name, Direction: domain.Downlink, State: state}}}
			switch state {
			case domain.TrafficResetAcknowledged:
				report.Items[1].Reset = &domain.TrafficCounterReset{PreviousBytes: 0, AcknowledgedAt: time.UnixMilli(200)}
				cancel()
			case domain.TrafficResetUnknown:
				return report, errors.Join(domain.ErrTrafficResetUnknown, domain.ErrAPIResourceLimit)
			case domain.TrafficResetNotAttempted:
				cancel()
				return report, context.Canceled
			}
			return report, nil
		})}
		response, err := h.ResetTrafficPair(ctx, &xrayv1.ResetTrafficPairRequest{InstanceId: " edge ", Scope: xrayv1.TrafficScope_TRAFFIC_SCOPE_OUTBOUND, Name: " exact>>>opaque "})
		if state == domain.TrafficResetUnknown || state == domain.TrafficResetNotAttempted {
			code := codes.ResourceExhausted
			if state == domain.TrafficResetNotAttempted {
				code = codes.Canceled
			}
			if response != nil || status.Code(err) != code {
				t.Fatalf("reset failure: %v %v", response, err)
			}
			details := status.Convert(err).Details()
			if len(details) != 1 {
				t.Fatalf("missing progress: %v", err)
			}
			failure, ok := details[0].(*xrayv1.TrafficPairResetFailure)
			if !ok {
				t.Fatalf("wrong detail: %v", details)
			}
			response = failure.Progress
		} else if err != nil {
			t.Fatal(err)
		}
		if response == nil || response.InstanceId != " edge " || response.Name != " exact>>>opaque " || len(response.Items) != 2 || response.Items[0].Acknowledgement == nil || response.Items[0].Acknowledgement.PreviousBytes != math.MaxInt64 {
			t.Fatalf("lost confirmed progress: %v", response)
		}
		want := map[domain.TrafficResetState]xrayv1.TrafficCounterResetState{domain.TrafficResetAcknowledged: xrayv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_ACKNOWLEDGED, domain.TrafficResetMissing: xrayv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_MISSING, domain.TrafficResetUnknown: xrayv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_UNKNOWN, domain.TrafficResetNotAttempted: xrayv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_NOT_ATTEMPTED}[state]
		if response.Items[1].State != want || (response.Items[1].Acknowledgement != nil) != (state == domain.TrafficResetAcknowledged) || state == domain.TrafficResetAcknowledged && response.Items[1].Acknowledgement.PreviousBytes != 0 {
			t.Fatalf("absence/unknown fabricated usage: %v", response)
		}
	}
}
