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

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/grpcapi"
	infogrpc "github.com/PapilioVPN/node/internal/modules/xray/info/grpcapi"
	"github.com/PapilioVPN/node/internal/modules/xray/traffic/domain"
	trafficgrpc "github.com/PapilioVPN/node/internal/modules/xray/traffic/grpcapi"
	trafficv1 "github.com/PapilioVPN/node/internal/modules/xray/traffic/grpcapi/proto/v1"
)

type trafficCountersReader func(context.Context, instances.InstanceID, domain.TrafficScope) (domain.InstanceTrafficCounters, error)

func (f trafficCountersReader) ListTrafficCounters(ctx context.Context, id instances.InstanceID, scope domain.TrafficScope) (domain.InstanceTrafficCounters, error) {
	return f(ctx, id, scope)
}

func TestTrafficCollectionAuthorizationAndShapePrecedeApplication(t *testing.T) {
	for _, test := range []struct {
		ctx   context.Context
		grant bool
		code  codes.Code
	}{
		{t.Context(), true, codes.Unauthenticated}, {verifiedContext(t), false, codes.PermissionDenied},
	} {
		h := grpcapi.Handler{TrafficAPI: trafficgrpc.TrafficAPI{AllowStatsRead: test.grant}, InfoAPI: infogrpc.InfoAPI{AllowInfoRead: true}}
		if _, err := h.ListTrafficCounters(test.ctx, nil); status.Code(err) != test.code {
			t.Fatalf("authorization order: %v", err)
		}
	}
	h := trafficgrpc.TrafficAPI{AllowStatsRead: true}
	for _, request := range []*trafficv1.ListTrafficCountersRequest{nil, {}, {InstanceId: " "}, {InstanceId: "edge"}, {InstanceId: "edge", Scope: 999}} {
		if _, err := h.ListTrafficCounters(verifiedContext(t), request); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid input reached application: %v", err)
		}
	}
}

func TestTrafficCollectionWirePreservesScopesItemsAndSignedBytes(t *testing.T) {
	ctx := verifiedContext(t)
	observed := time.UnixMilli(1700000000123).UTC()
	for wire, scope := range map[trafficv1.TrafficScope]domain.TrafficScope{
		trafficv1.TrafficScope_TRAFFIC_SCOPE_USER: domain.UserTraffic, trafficv1.TrafficScope_TRAFFIC_SCOPE_INBOUND: domain.InboundTraffic, trafficv1.TrafficScope_TRAFFIC_SCOPE_OUTBOUND: domain.OutboundTraffic,
	} {
		for _, empty := range []bool{false, true} {
			h := trafficgrpc.TrafficAPI{AllowStatsRead: true, TrafficCounters: trafficCountersReader(func(received context.Context, id instances.InstanceID, selection domain.TrafficScope) (domain.InstanceTrafficCounters, error) {
				if received != ctx || id != " edge " || selection != scope {
					t.Fatal("changed input/context")
				}
				counters := domain.TrafficCounters{ObservedAt: observed}
				if !empty {
					counters.Items = []domain.TrafficCounterEntry{
						{Name: " Mixed>>>@Example ", Direction: domain.Downlink, Bytes: math.MinInt64},
						{Name: " Mixed>>>@Example ", Direction: domain.Uplink, Bytes: math.MaxInt64},
						{Name: " Mixed>>>@Example ", Direction: domain.Uplink, Bytes: 0},
					}
				}
				return domain.InstanceTrafficCounters{InstanceID: id, Scope: scope, Counters: counters}, nil
			})}
			response, err := h.ListTrafficCounters(ctx, &trafficv1.ListTrafficCountersRequest{InstanceId: " edge ", Scope: wire})
			if err != nil || response.InstanceId != " edge " || response.Scope != wire || response.ObservedAtUnixMs != observed.UnixMilli() {
				t.Fatalf("wire facts: %v, %v", response, err)
			}
			if empty {
				if len(response.Items) != 0 {
					t.Fatal("empty observation fabricated items")
				}
				continue
			}
			if len(response.Items) != 3 {
				t.Fatalf("changed count: %v", response)
			}
			for i, item := range response.Items {
				direction := trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK
				if i == 0 {
					direction = trafficv1.TrafficDirection_TRAFFIC_DIRECTION_DOWNLINK
				}
				if item.Name != " Mixed>>>@Example " || item.Direction != direction || item.Bytes != []int64{math.MinInt64, math.MaxInt64, 0}[i] {
					t.Fatalf("changed item: %v", item)
				}
			}
		}
	}
}

func TestTrafficCollectionFailuresAreSanitizedWithoutPartialSuccess(t *testing.T) {
	for cause, code := range map[error]codes.Code{
		instances.ErrNotFound: codes.NotFound, domain.ErrAPIDisabled: codes.FailedPrecondition, domain.ErrTrafficUnsupported: codes.FailedPrecondition,
		domain.ErrAPIBusy: codes.ResourceExhausted, domain.ErrAPIResourceLimit: codes.ResourceExhausted, domain.ErrInvalidTrafficResponse: codes.Unavailable,
		context.Canceled: codes.Canceled, context.DeadlineExceeded: codes.DeadlineExceeded, errors.New("private endpoint"): codes.Unavailable,
	} {
		h := trafficgrpc.TrafficAPI{AllowStatsRead: true, TrafficCounters: trafficCountersReader(func(context.Context, instances.InstanceID, domain.TrafficScope) (domain.InstanceTrafficCounters, error) {
			return domain.InstanceTrafficCounters{Counters: domain.TrafficCounters{Items: []domain.TrafficCounterEntry{{Bytes: 99}}}}, fmt.Errorf("private endpoint: %w", cause)
		})}
		response, err := h.ListTrafficCounters(verifiedContext(t), &trafficv1.ListTrafficCountersRequest{InstanceId: "edge", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_USER})
		if response != nil || status.Code(err) != code || strings.Contains(err.Error(), "private") {
			t.Fatalf("wire failure: %v, %v", response, err)
		}
	}
}
