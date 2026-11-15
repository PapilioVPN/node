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

type scopeResetter func(context.Context, instances.InstanceID, domain.TrafficScope, int) (domain.InstanceTrafficCountersReset, error)

func (f scopeResetter) ResetTrafficCounters(ctx context.Context, id instances.InstanceID, scope domain.TrafficScope, limit int) (domain.InstanceTrafficCountersReset, error) {
	return f(ctx, id, scope, limit)
}

func TestScopeResetRequiresIndependentWriteGrantAndPositiveBoundBeforeSelection(t *testing.T) {
	if _, err := (grpcapi.Handler{AllowStatsReset: true}).ResetTrafficCounters(t.Context(), nil); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("identity: %v", err)
	}
	if _, err := (grpcapi.Handler{AllowStatsRead: true, AllowInfoRead: true}).ResetTrafficCounters(verifiedContext(t), nil); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("grant: %v", err)
	}
	h := grpcapi.Handler{AllowStatsReset: true}
	for _, request := range []*xrayv1.ResetTrafficCountersRequest{nil, {}, {InstanceId: "edge", Scope: xrayv1.TrafficScope_TRAFFIC_SCOPE_USER}, {InstanceId: " ", Scope: xrayv1.TrafficScope_TRAFFIC_SCOPE_USER, MaxCounters: 1}, {InstanceId: "edge", Scope: 999, MaxCounters: 1}} {
		if _, err := h.ResetTrafficCounters(verifiedContext(t), request); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("shape reached service: %v", err)
		}
	}
}

func TestScopeResetWireRetainsAcknowledgedZeroSignedBytesAndPartialStates(t *testing.T) {
	for _, cause := range []error{nil, domain.ErrTrafficCounterNotFound, errors.Join(domain.ErrTrafficResetUnknown, domain.ErrAPIResourceLimit)} {
		ctx, cancel := context.WithCancel(verifiedContext(t))
		defer cancel()
		observed, acknowledged := time.UnixMilli(1700000000000), time.UnixMilli(1700000000001)
		record := domain.InstanceTrafficCountersReset{InstanceID: " edge ", Scope: domain.UserTraffic, Reset: domain.TrafficCountersReset{SelectedAt: observed, Items: []domain.TrafficCounterResetEntry{
			{Name: " exact>>>opaque ", Direction: domain.Uplink, State: domain.TrafficResetAcknowledged, Reset: &domain.TrafficCounterReset{PreviousBytes: math.MinInt64, AcknowledgedAt: acknowledged}},
			{Name: "b", Direction: domain.Downlink, State: domain.TrafficResetAcknowledged, Reset: &domain.TrafficCounterReset{PreviousBytes: 0, AcknowledgedAt: acknowledged}},
		}}}
		if cause != nil {
			state := domain.TrafficResetNotApplied
			if errors.Is(cause, domain.ErrTrafficResetUnknown) {
				state = domain.TrafficResetUnknown
			}
			record.Reset.Items = append(record.Reset.Items, domain.TrafficCounterResetEntry{Name: "c", Direction: domain.Uplink, State: state}, domain.TrafficCounterResetEntry{Name: "d", Direction: domain.Downlink, State: domain.TrafficResetNotAttempted})
		}
		h := grpcapi.Handler{AllowStatsReset: true, ScopeResets: scopeResetter(func(received context.Context, id instances.InstanceID, scope domain.TrafficScope, limit int) (domain.InstanceTrafficCountersReset, error) {
			if received != ctx || id != " edge " || scope != domain.UserTraffic || limit != math.MaxUint32 {
				t.Fatal("input/context changed")
			}
			if cause == nil {
				cancel()
				return record, nil
			}
			return record, errors.Join(cause, errors.New("private-native-diagnostics"))
		})}
		response, err := h.ResetTrafficCounters(ctx, &xrayv1.ResetTrafficCountersRequest{InstanceId: " edge ", Scope: xrayv1.TrafficScope_TRAFFIC_SCOPE_USER, MaxCounters: math.MaxUint32})
		if cause != nil {
			want := codes.NotFound
			if errors.Is(cause, domain.ErrAPIResourceLimit) {
				want = codes.ResourceExhausted
			}
			if response != nil || status.Code(err) != want || strings.Contains(err.Error(), "private") {
				t.Fatalf("partial failure: %v %v", response, err)
			}
			details := status.Convert(err).Details()
			if len(details) != 1 {
				t.Fatalf("progress detail missing: %v", err)
			}
			failure, ok := details[0].(*xrayv1.TrafficCountersResetFailure)
			if !ok {
				t.Fatalf("wrong detail: %v", details)
			}
			response = failure.Progress
		} else if err != nil {
			t.Fatal(err)
		}
		if response == nil || response.InstanceId != " edge " || response.Scope != xrayv1.TrafficScope_TRAFFIC_SCOPE_USER || response.SelectedAtUnixMs == nil || response.GetSelectedAtUnixMs() != observed.UnixMilli() || len(response.Items) != len(record.Reset.Items) {
			t.Fatalf("report: %v", response)
		}
		if response.Items[0].Name != " exact>>>opaque " || response.Items[0].State != xrayv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_ACKNOWLEDGED || response.Items[0].Acknowledgement.PreviousBytes != math.MinInt64 || response.Items[0].Acknowledgement.AcknowledgedAtUnixMs != acknowledged.UnixMilli() || response.Items[1].Acknowledgement == nil || response.Items[1].Acknowledgement.PreviousBytes != 0 {
			t.Fatalf("acknowledgement lost: %v", response)
		}
		if cause != nil {
			want := xrayv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_NOT_APPLIED
			if errors.Is(cause, domain.ErrTrafficResetUnknown) {
				want = xrayv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_UNKNOWN
			}
			if response.Items[2].State != want || response.Items[2].Acknowledgement != nil || response.Items[3].State != xrayv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_NOT_ATTEMPTED || response.Items[3].Acknowledgement != nil {
				t.Fatalf("unconfirmed progress changed: %v", response)
			}
		}
	}
}

func TestScopeResetPreflightFailureDoesNotInventDiscoveryOrEffects(t *testing.T) {
	h := grpcapi.Handler{AllowStatsReset: true, ScopeResets: scopeResetter(func(context.Context, instances.InstanceID, domain.TrafficScope, int) (domain.InstanceTrafficCountersReset, error) {
		return domain.InstanceTrafficCountersReset{InstanceID: "edge", Scope: domain.UserTraffic}, domain.ErrAPIResourceLimit
	})}
	_, err := h.ResetTrafficCounters(verifiedContext(t), &xrayv1.ResetTrafficCountersRequest{InstanceId: "edge", Scope: xrayv1.TrafficScope_TRAFFIC_SCOPE_USER, MaxCounters: 1})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatal(err)
	}
	details := status.Convert(err).Details()
	if len(details) != 1 {
		t.Fatalf("no progress detail: %v", err)
	}
	progress := details[0].(*xrayv1.TrafficCountersResetFailure).Progress
	if progress == nil || progress.SelectedAtUnixMs != nil || len(progress.Items) != 0 {
		t.Fatalf("preflight invented discovery/effects: %v", progress)
	}
}
