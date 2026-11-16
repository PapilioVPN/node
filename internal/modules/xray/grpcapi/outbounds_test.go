package grpcapi_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/grpcapi"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/xray/grpcapi/proto/v1"
	infogrpc "github.com/PapilioVPN/node/internal/modules/xray/info/grpcapi"
	routinggrpc "github.com/PapilioVPN/node/internal/modules/xray/routing/grpcapi"
	trafficgrpc "github.com/PapilioVPN/node/internal/modules/xray/traffic/grpcapi"
	usergrpc "github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi"
)

type outboundStatusReader struct {
	get  func(context.Context, instances.InstanceID, string) (domain.InstanceOutboundObservation, error)
	list func(context.Context, instances.InstanceID) (domain.InstanceOutboundStatuses, error)
}

func (r outboundStatusReader) GetOutboundStatus(ctx context.Context, id instances.InstanceID, tag string) (domain.InstanceOutboundObservation, error) {
	return r.get(ctx, id, tag)
}
func (r outboundStatusReader) ListOutboundStatuses(ctx context.Context, id instances.InstanceID) (domain.InstanceOutboundStatuses, error) {
	return r.list(ctx, id)
}

func TestOutboundReadsRequireIdentityBeforeShape(t *testing.T) {
	h := grpcapi.Handler{}
	if _, err := h.GetOutboundStatus(t.Context(), nil); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unverified single read reached shape/application: %v", err)
	}
	if _, err := h.ListOutboundStatuses(t.Context(), nil); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unverified collection reached shape/application: %v", err)
	}
}

func TestOutboundReadsRequireIndependentGrantBeforeShape(t *testing.T) {
	h := grpcapi.Handler{AllowOnlineRead: true, UserAPI: usergrpc.UserAPI{AllowUserManagement: true, AllowUsersRead: true}, RoutingAPI: routinggrpc.RoutingAPI{AllowRoutingRead: true, AllowRoutingTest: true, AllowRoutingManagement: true}, TrafficAPI: trafficgrpc.TrafficAPI{AllowStatsRead: true, AllowStatsReset: true}, InfoAPI: infogrpc.InfoAPI{AllowInfoRead: true}}
	if _, err := h.GetOutboundStatus(verifiedContext(t), nil); status.Code(err) != codes.PermissionDenied {
		t.Fatal("other grants enabled outbound lookup")
	}
	if _, err := h.ListOutboundStatuses(verifiedContext(t), nil); status.Code(err) != codes.PermissionDenied {
		t.Fatal("other grants enabled outbound collection")
	}
	h = grpcapi.Handler{AllowOutboundsRead: true}
	for _, r := range []*xrayv1.GetOutboundStatusRequest{nil, {}, {InstanceId: " "}, {InstanceId: "edge"}, {InstanceId: "edge", OutboundTag: proto.String("\xff")}} {
		if _, err := h.GetOutboundStatus(verifiedContext(t), r); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid selection reached application: %v", err)
		}
	}
	for _, r := range []*xrayv1.ListOutboundStatusesRequest{nil, {}, {InstanceId: " "}} {
		if _, err := h.ListOutboundStatuses(verifiedContext(t), r); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid collection reached application: %v", err)
		}
	}
}

func TestOutboundSingleWireRetainsAbsenceZeroHealthAndNativePrecision(t *testing.T) {
	for _, tag := range []string{"", "   ", " opaque>>>tag "} {
		for _, value := range []*domain.OutboundStatus{nil, {}, {Alive: true, DelayMS: math.MaxInt64, LastSeenUnixSec: math.MinInt64, LastTryUnixSec: math.MaxInt64, LastErrorReason: "private native diagnostic", HealthPing: &domain.OutboundHealthPing{TotalSamples: math.MaxInt64, FailedSamples: math.MinInt64, DeviationNS: math.MaxInt64, AverageNS: 1234567, MaxNS: math.MaxInt64, MinNS: math.MinInt64}}, {HealthPing: &domain.OutboundHealthPing{}}} {
			ctx, observed, calls := verifiedContext(t), time.UnixMilli(1700000000123), 0
			if value != nil {
				value.Tag = tag
			}
			h := grpcapi.Handler{AllowOutboundsRead: true, Outbounds: outboundStatusReader{get: func(received context.Context, id instances.InstanceID, selected string) (domain.InstanceOutboundObservation, error) {
				calls++
				if received != ctx || id != " edge " || selected != tag {
					t.Fatal("lookup changed exact identity/context")
				}
				return domain.InstanceOutboundObservation{InstanceID: id, Tag: tag, Observation: domain.OutboundObservation{Status: value, ObservedAt: observed}}, nil
			}}}
			response, err := h.GetOutboundStatus(ctx, &xrayv1.GetOutboundStatusRequest{InstanceId: " edge ", OutboundTag: proto.String(tag)})
			if err != nil || calls != 1 || response.InstanceId != " edge " || response.OutboundTag != tag || response.ObservedAtUnixMs != observed.UnixMilli() {
				t.Fatalf("single result changed: %v %v", response, err)
			}
			encoded, err := proto.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			var decoded xrayv1.GetOutboundStatusResponse
			if err := proto.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "private native diagnostic") || (decoded.Status == nil) != (value == nil) {
				t.Fatal("wire lost status presence or exposed native diagnostics")
			}
			if value != nil {
				assertOutboundWire(t, decoded.Status, *value)
			}
		}
	}
}

func TestOutboundCollectionKeepsCompleteAppOrderAndDuplicates(t *testing.T) {
	for _, count := range []int{0, 1, 100, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			items := make([]domain.OutboundStatus, count)
			for i := range items {
				items[i] = domain.OutboundStatus{Tag: fmt.Sprint("opaque-", count-i%7), DelayMS: int64(i), HealthPing: &domain.OutboundHealthPing{AverageNS: int64(i) * 1000}}
			}
			calls, observed := 0, time.UnixMilli(1700000000123)
			h := grpcapi.Handler{AllowOutboundsRead: true, Outbounds: outboundStatusReader{list: func(_ context.Context, id instances.InstanceID) (domain.InstanceOutboundStatuses, error) {
				calls++
				if id != " edge " {
					t.Fatal("collection identity changed")
				}
				return domain.InstanceOutboundStatuses{InstanceID: id, Statuses: domain.OutboundStatuses{Items: items, ObservedAt: observed}}, nil
			}}}
			result, err := h.ListOutboundStatuses(verifiedContext(t), &xrayv1.ListOutboundStatusesRequest{InstanceId: " edge "})
			if err != nil || calls != 1 || len(result.Statuses) != count || result.InstanceId != " edge " || result.ObservedAtUnixMs != observed.UnixMilli() {
				t.Fatalf("collection truncated/refetched: %v", err)
			}
			for i, value := range items {
				assertOutboundWire(t, result.Statuses[i], value)
			}
		})
	}
}

func assertOutboundWire(t *testing.T, wire *xrayv1.OutboundStatus, value domain.OutboundStatus) {
	t.Helper()
	if wire.OutboundTag != value.Tag || wire.Alive != value.Alive || wire.DelayMs != value.DelayMS || wire.LastSeenUnixSec != value.LastSeenUnixSec || wire.LastTryUnixSec != value.LastTryUnixSec || (wire.HealthPing == nil) != (value.HealthPing == nil) {
		t.Fatal("native fields/units/presence changed on wire")
	}
	if h := value.HealthPing; h != nil {
		w := wire.HealthPing
		if w.TotalSamples != h.TotalSamples || w.FailedSamples != h.FailedSamples || w.DeviationNs != h.DeviationNS || w.AverageNs != h.AverageNS || w.MaxNs != h.MaxNS || w.MinNs != h.MinNS {
			t.Fatal("burst precision or native nanosecond units changed")
		}
	}
}

func TestOutboundReadErrorsAndLateCancellationDiscardPartialFacts(t *testing.T) {
	for _, tc := range []struct {
		cause error
		code  codes.Code
	}{
		{instances.ErrNotFound, codes.NotFound}, {domain.ErrAPIDisabled, codes.FailedPrecondition}, {domain.ErrOutboundObservationUnsupported, codes.FailedPrecondition},
		{domain.ErrAPIBusy, codes.ResourceExhausted}, {domain.ErrAPIResourceLimit, codes.ResourceExhausted}, {domain.ErrInvalidOutboundObservation, codes.Unavailable},
		{errors.New("private native reason"), codes.Unavailable}, {context.Canceled, codes.Canceled}, {context.DeadlineExceeded, codes.DeadlineExceeded}, {nil, codes.Canceled},
	} {
		ctx, cancel := context.WithCancel(verifiedContext(t))
		cause := tc.cause
		h := grpcapi.Handler{AllowOutboundsRead: true, Outbounds: outboundStatusReader{
			get: func(context.Context, instances.InstanceID, string) (domain.InstanceOutboundObservation, error) {
				if cause == nil {
					cancel()
				}
				return domain.InstanceOutboundObservation{InstanceID: "partial", Observation: domain.OutboundObservation{Status: &domain.OutboundStatus{}}}, cause
			},
			list: func(context.Context, instances.InstanceID) (domain.InstanceOutboundStatuses, error) {
				if cause == nil {
					cancel()
				}
				return domain.InstanceOutboundStatuses{InstanceID: "partial", Statuses: domain.OutboundStatuses{Items: []domain.OutboundStatus{{}}}}, cause
			},
		}}
		response, err := h.GetOutboundStatus(ctx, &xrayv1.GetOutboundStatusRequest{InstanceId: "edge", OutboundTag: proto.String("")})
		if response != nil || status.Code(err) != tc.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("failed single exposed partial facts/cause: %v %v", response, err)
		}
		cancel()
		ctx, cancel = context.WithCancel(verifiedContext(t))
		page, err := h.ListOutboundStatuses(ctx, &xrayv1.ListOutboundStatusesRequest{InstanceId: "edge"})
		if page != nil || status.Code(err) != tc.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("failed collection exposed partial facts/cause: %v %v", page, err)
		}
		cancel()
	}
}
