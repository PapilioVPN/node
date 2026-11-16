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
	"github.com/PapilioVPN/node/internal/modules/core/traffic/domain"
	trafficgrpc "github.com/PapilioVPN/node/internal/modules/core/traffic/grpcapi"
	trafficv1 "github.com/PapilioVPN/node/internal/modules/core/traffic/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi"
)

type trafficReader func(context.Context, instances.InstanceID, domain.TrafficSelection) (domain.InstanceTrafficCounter, error)

func (f trafficReader) GetTrafficCounter(ctx context.Context, id instances.InstanceID, selection domain.TrafficSelection) (domain.InstanceTrafficCounter, error) {
	return f(ctx, id, selection)
}

func TestTrafficIdentityGrantAndShapePrecedeApplication(t *testing.T) {
	for _, test := range []struct {
		ctx   context.Context
		grant bool
		code  codes.Code
	}{
		{t.Context(), true, codes.Unauthenticated}, {verifiedContext(t), false, codes.PermissionDenied},
	} {
		h := grpcapi.Handler{AllowInfoRead: true, TrafficAPI: trafficgrpc.TrafficAPI{AllowStatsRead: test.grant}}
		if _, err := h.GetTrafficCounter(test.ctx, nil); status.Code(err) != test.code {
			t.Fatalf("authorization before shape: %v", err)
		}
	}
	h := trafficgrpc.TrafficAPI{AllowStatsRead: true}
	for _, request := range []*trafficv1.GetTrafficCounterRequest{
		nil, {}, {InstanceId: " "},
		{InstanceId: "edge", Name: "tag", Scope: 999, Direction: trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK},
		{InstanceId: "edge", Name: "tag", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_INBOUND, Direction: 999},
		{InstanceId: "edge", Name: "tag", Direction: trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK},
		{InstanceId: "edge", Name: "tag", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_INBOUND},
		{InstanceId: "edge", Name: " ", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_INBOUND, Direction: trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK},
	} {
		if _, err := h.GetTrafficCounter(verifiedContext(t), request); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid shape reached application: %v", err)
		}
	}
}

func TestTrafficWirePreservesAllSelectionsAndSignedBytes(t *testing.T) {
	ctx := verifiedContext(t)
	observed := time.UnixMilli(1700000000123).UTC()
	for scope, selected := range map[trafficv1.TrafficScope]domain.TrafficScope{
		trafficv1.TrafficScope_TRAFFIC_SCOPE_USER:     domain.UserTraffic,
		trafficv1.TrafficScope_TRAFFIC_SCOPE_INBOUND:  domain.InboundTraffic,
		trafficv1.TrafficScope_TRAFFIC_SCOPE_OUTBOUND: domain.OutboundTraffic,
	} {
		for direction, link := range map[trafficv1.TrafficDirection]domain.TrafficDirection{
			trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK:   domain.Uplink,
			trafficv1.TrafficDirection_TRAFFIC_DIRECTION_DOWNLINK: domain.Downlink,
		} {
			for _, value := range []int64{0, math.MinInt64, math.MaxInt64} {
				h := trafficgrpc.TrafficAPI{AllowStatsRead: true, Traffic: trafficReader(func(received context.Context, id instances.InstanceID, selection domain.TrafficSelection) (domain.InstanceTrafficCounter, error) {
					if received != ctx || id != " edge " || selection != (domain.TrafficSelection{Scope: selected, Name: " Mixed@Example ", Direction: link}) {
						t.Fatal("identity/selection/context changed")
					}
					return domain.InstanceTrafficCounter{InstanceID: id, Selection: selection, Counter: domain.TrafficCounter{Bytes: value, ObservedAt: observed}}, nil
				})}
				response, err := h.GetTrafficCounter(ctx, &trafficv1.GetTrafficCounterRequest{InstanceId: " edge ", Scope: scope, Name: " Mixed@Example ", Direction: direction})
				if err != nil || response.InstanceId != " edge " || response.Scope != scope || response.Name != " Mixed@Example " || response.Direction != direction || response.Bytes != value || response.ObservedAtUnixMs != observed.UnixMilli() {
					t.Fatalf("wire facts: %v, %v", response, err)
				}
			}
		}
	}
}

func TestTrafficErrorsAreSanitizedAndDiscardPartialFacts(t *testing.T) {
	for cause, expected := range map[error]codes.Code{
		instances.ErrNotFound: codes.NotFound, domain.ErrTrafficCounterNotFound: codes.NotFound,
		domain.ErrAPIDisabled: codes.FailedPrecondition, domain.ErrTrafficUnsupported: codes.FailedPrecondition,
		domain.ErrAPIBusy: codes.ResourceExhausted, domain.ErrAPIResourceLimit: codes.ResourceExhausted,
		context.Canceled: codes.Canceled, context.DeadlineExceeded: codes.DeadlineExceeded,
		domain.ErrInvalidTrafficResponse: codes.Unavailable, errors.New("private endpoint"): codes.Unavailable,
	} {
		h := trafficgrpc.TrafficAPI{AllowStatsRead: true, Traffic: trafficReader(func(context.Context, instances.InstanceID, domain.TrafficSelection) (domain.InstanceTrafficCounter, error) {
			return domain.InstanceTrafficCounter{Counter: domain.TrafficCounter{Bytes: 99}}, fmt.Errorf("private endpoint: %w", cause)
		})}
		response, err := h.GetTrafficCounter(verifiedContext(t), &trafficv1.GetTrafficCounterRequest{InstanceId: "edge", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_USER, Name: "user", Direction: trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK})
		if response != nil || status.Code(err) != expected || strings.Contains(err.Error(), "private") {
			t.Fatalf("sanitized failure: %v, %v; want %v", response, err, expected)
		}
	}
}
