package grpcapi_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/grpcapi"
	infogrpc "github.com/PapilioVPN/node/internal/modules/xray/info/grpcapi"
	"github.com/PapilioVPN/node/internal/modules/xray/routing/domain"
	routinggrpc "github.com/PapilioVPN/node/internal/modules/xray/routing/grpcapi"
	routingv1 "github.com/PapilioVPN/node/internal/modules/xray/routing/grpcapi/proto/v1"
	trafficgrpc "github.com/PapilioVPN/node/internal/modules/xray/traffic/grpcapi"
	usergrpc "github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi"
)

type routingReader struct {
	list     func(context.Context, instances.InstanceID) (domain.InstanceRoutingRules, error)
	balancer func(context.Context, instances.InstanceID, string) (domain.InstanceBalancerInfo, error)
}

func (r routingReader) ListRoutingRules(ctx context.Context, id instances.InstanceID) (domain.InstanceRoutingRules, error) {
	return r.list(ctx, id)
}

func (r routingReader) GetBalancerInfo(ctx context.Context, id instances.InstanceID, tag string) (domain.InstanceBalancerInfo, error) {
	return r.balancer(ctx, id, tag)
}

func TestRoutingReadsRequireIdentityAndIndependentGrantBeforeShape(t *testing.T) {
	h := grpcapi.Handler{}
	if _, err := h.ListRoutingRules(t.Context(), nil); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unverified list reached shape/application: %v", err)
	}
	if _, err := h.GetBalancerInfo(t.Context(), nil); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unverified balancer read reached shape/application: %v", err)
	}
	h = grpcapi.Handler{UserAPI: usergrpc.UserAPI{AllowUsersRead: true, AllowUserManagement: true, AllowOnlineRead: true}, TrafficAPI: trafficgrpc.TrafficAPI{AllowStatsRead: true, AllowStatsReset: true}, InfoAPI: infogrpc.InfoAPI{AllowInfoRead: true}}
	if _, err := h.ListRoutingRules(verifiedContext(t), nil); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other grants enabled routing list: %v", err)
	}
	if _, err := h.GetBalancerInfo(verifiedContext(t), nil); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other grants enabled balancer read: %v", err)
	}
}

func TestRoutingListPreservesPriorityEmptyTagsDuplicatesAndObservation(t *testing.T) {
	for _, items := range [][]domain.RoutingRuleReference{nil, {
		{OutboundTag: "z-first", RuleTag: "z"}, {OutboundTag: "a-second", RuleTag: "a"},
		{}, {OutboundTag: "", RuleTag: "a"}, {OutboundTag: "z-first", RuleTag: "z"},
	}} {
		ctx, observed, calls := verifiedContext(t), time.UnixMilli(1700000000123), 0
		h := grpcapi.Handler{RoutingAPI: routinggrpc.RoutingAPI{AllowRoutingRead: true, Routing: routingReader{list: func(received context.Context, id instances.InstanceID) (domain.InstanceRoutingRules, error) {
			calls++
			if received != ctx || id != " edge " {
				t.Fatal("list selection/context changed")
			}
			return domain.InstanceRoutingRules{InstanceID: id, Rules: domain.RoutingRules{Items: items, ObservedAt: observed}}, nil
		}}}}
		response, err := h.ListRoutingRules(ctx, &routingv1.ListRoutingRulesRequest{InstanceId: " edge "})
		if err != nil || calls != 1 || response.InstanceId != " edge " || response.ObservedAtUnixMs != observed.UnixMilli() || len(response.Rules) != len(items) {
			t.Fatalf("routing list changed: %v %v", response, err)
		}
		for i, item := range items {
			if response.Rules[i].OutboundTag != item.OutboundTag || response.Rules[i].RuleTag != item.RuleTag {
				t.Fatalf("priority/reference changed at %d: %v", i, response)
			}
		}
	}
}

func TestBalancerWirePreservesOptionalEmptyAndOpaqueTargets(t *testing.T) {
	empty, override := "", " exact>>>target "
	for _, target := range []*string{nil, &empty, &override} {
		for _, targets := range []*domain.BalancerTargets{nil, {}, {Tags: []string{"z", "a", "a", ""}}} {
			ctx, observed, calls := verifiedContext(t), time.UnixMilli(1700000000123), 0
			h := grpcapi.Handler{RoutingAPI: routinggrpc.RoutingAPI{AllowRoutingRead: true, Routing: routingReader{balancer: func(received context.Context, id instances.InstanceID, tag string) (domain.InstanceBalancerInfo, error) {
				calls++
				if received != ctx || id != " edge " || tag != " pool>>>opaque " {
					t.Fatal("balancer selection/context changed")
				}
				return domain.InstanceBalancerInfo{InstanceID: id, Tag: tag, Balancer: domain.BalancerInfo{OverrideTarget: target, PrincipalTargets: targets, ObservedAt: observed}}, nil
			}}}}
			response, err := h.GetBalancerInfo(ctx, &routingv1.GetBalancerInfoRequest{InstanceId: " edge ", BalancerTag: " pool>>>opaque "})
			if err != nil || calls != 1 {
				t.Fatalf("balancer read: %v %v", response, err)
			}
			encoded, err := proto.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			var received routingv1.GetBalancerInfoResponse
			if err := proto.Unmarshal(encoded, &received); err != nil {
				t.Fatal(err)
			}
			if received.InstanceId != " edge " || received.BalancerTag != " pool>>>opaque " || received.ObservedAtUnixMs != observed.UnixMilli() || !reflect.DeepEqual(received.OverrideTarget, target) || (received.PrincipalTargets == nil) != (targets == nil) {
				t.Fatalf("wire lost identity/presence: %v", &received)
			}
			if targets != nil && !reflect.DeepEqual(received.PrincipalTargets.Tags, targets.Tags) {
				t.Fatalf("wire lost candidate order/duplicates: %v", &received)
			}
		}
	}
}

func TestRoutingReadsRejectInvalidSelectionBeforeApplication(t *testing.T) {
	h := grpcapi.Handler{RoutingAPI: routinggrpc.RoutingAPI{AllowRoutingRead: true}}
	for _, request := range []*routingv1.ListRoutingRulesRequest{nil, {}, {InstanceId: " "}} {
		if _, err := h.ListRoutingRules(verifiedContext(t), request); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid list selection reached application: %v", err)
		}
	}
	for _, request := range []*routingv1.GetBalancerInfoRequest{nil, {}, {InstanceId: " ", BalancerTag: "pool"}, {InstanceId: "edge", BalancerTag: " "}, {InstanceId: "edge", BalancerTag: "\xff"}} {
		if _, err := h.GetBalancerInfo(verifiedContext(t), request); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid balancer selection reached application: %v", err)
		}
	}
}

func TestRoutingReadFailuresAreSanitizedAndDiscardPartialFacts(t *testing.T) {
	for _, tc := range []struct {
		cause error
		code  codes.Code
	}{
		{instances.ErrNotFound, codes.NotFound}, {domain.ErrRoutingTargetNotFound, codes.NotFound},
		{domain.ErrAPIDisabled, codes.FailedPrecondition}, {domain.ErrRoutingReadUnsupported, codes.FailedPrecondition},
		{domain.ErrAPIBusy, codes.ResourceExhausted}, {domain.ErrAPIResourceLimit, codes.ResourceExhausted},
		{domain.ErrInvalidRoutingResponse, codes.Unavailable}, {errors.New("private endpoint"), codes.Unavailable},
		{context.Canceled, codes.Canceled}, {context.DeadlineExceeded, codes.DeadlineExceeded},
	} {
		err := errors.Join(tc.cause, errors.New("private endpoint/credentials"))
		h := grpcapi.Handler{RoutingAPI: routinggrpc.RoutingAPI{AllowRoutingRead: true, Routing: routingReader{
			list: func(context.Context, instances.InstanceID) (domain.InstanceRoutingRules, error) {
				return domain.InstanceRoutingRules{InstanceID: "partial"}, err
			},
			balancer: func(context.Context, instances.InstanceID, string) (domain.InstanceBalancerInfo, error) {
				return domain.InstanceBalancerInfo{InstanceID: "partial"}, err
			},
		}}}
		if result, err := h.ListRoutingRules(verifiedContext(t), &routingv1.ListRoutingRulesRequest{InstanceId: "edge"}); result != nil || status.Code(err) != tc.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("failed list returned partial facts/diagnostics: %v %v", result, err)
		}
		if result, err := h.GetBalancerInfo(verifiedContext(t), &routingv1.GetBalancerInfoRequest{InstanceId: "edge", BalancerTag: "pool"}); result != nil || status.Code(err) != tc.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("failed balancer returned partial facts/diagnostics: %v %v", result, err)
		}
	}
}

func TestRoutingReadsDiscardFactsOnLateCancellation(t *testing.T) {
	for _, list := range []bool{false, true} {
		ctx, cancel := context.WithCancel(verifiedContext(t))
		defer cancel()
		h := grpcapi.Handler{RoutingAPI: routinggrpc.RoutingAPI{AllowRoutingRead: true, Routing: routingReader{
			list: func(context.Context, instances.InstanceID) (domain.InstanceRoutingRules, error) {
				cancel()
				return domain.InstanceRoutingRules{InstanceID: "edge"}, nil
			},
			balancer: func(context.Context, instances.InstanceID, string) (domain.InstanceBalancerInfo, error) {
				cancel()
				return domain.InstanceBalancerInfo{InstanceID: "edge"}, nil
			},
		}}}
		if list {
			if response, err := h.ListRoutingRules(ctx, &routingv1.ListRoutingRulesRequest{InstanceId: "edge"}); response != nil || status.Code(err) != codes.Canceled {
				t.Fatalf("canceled list became success: %v %v", response, err)
			}
		} else {
			if response, err := h.GetBalancerInfo(ctx, &routingv1.GetBalancerInfoRequest{InstanceId: "edge", BalancerTag: "pool"}); response != nil || status.Code(err) != codes.Canceled {
				t.Fatalf("canceled balancer read became success: %v %v", response, err)
			}
		}
	}
}

// Controlled peer for unit tests; bootstrap tests exercise actual mTLS.
func verifiedContext(t *testing.T) context.Context {
	t.Helper()
	return peer.NewContext(t.Context(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}}})
}
