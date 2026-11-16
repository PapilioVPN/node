package grpcapi_test

import (
	"context"
	"errors"
	"math"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	grpcapi "github.com/PapilioVPN/node/internal/bootstrap/xrayapi"
	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	infogrpc "github.com/PapilioVPN/node/internal/modules/xray/info/grpcapi"
	"github.com/PapilioVPN/node/internal/modules/xray/routing/domain"
	routinggrpc "github.com/PapilioVPN/node/internal/modules/xray/routing/grpcapi"
	routingv1 "github.com/PapilioVPN/node/internal/modules/xray/routing/grpcapi/proto/v1"
	trafficgrpc "github.com/PapilioVPN/node/internal/modules/xray/traffic/grpcapi"
	usergrpc "github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi"
)

type routingProber func(context.Context, instances.InstanceID, domain.RoutingProbe) (domain.InstanceRoutingProbe, error)

func (f routingProber) TestRoute(ctx context.Context, id instances.InstanceID, probe domain.RoutingProbe) (domain.InstanceRoutingProbe, error) {
	return f(ctx, id, probe)
}

func requireProbeOutcome(t *testing.T, err error, want routingv1.RoutingProbeOutcome) {
	t.Helper()
	details := status.Convert(err).Details()
	if len(details) != 1 {
		t.Fatalf("probe outcome detail missing: %v", err)
	}
	failure, ok := details[0].(*routingv1.TestRouteFailure)
	if !ok || failure.Outcome != want {
		t.Fatalf("probe effects misreported: %v want %v", details, want)
	}
}

func TestRouteRequiresIdentityAndIndependentGrantBeforeShape(t *testing.T) {
	if _, err := (grpcapi.Handler{}).TestRoute(t.Context(), nil); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unverified probe reached shape/application: %v", err)
	}
	h := grpcapi.Handler{UserAPI: usergrpc.UserAPI{AllowUsersRead: true, AllowUserManagement: true, AllowOnlineRead: true}, RoutingAPI: routinggrpc.RoutingAPI{AllowRoutingRead: true}, TrafficAPI: trafficgrpc.TrafficAPI{AllowStatsRead: true, AllowStatsReset: true}, InfoAPI: infogrpc.InfoAPI{AllowInfoRead: true}}
	if _, err := h.TestRoute(verifiedContext(t), nil); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other grants enabled routing probes: %v", err)
	}
}

func TestRouteRejectsInvalidShapeBeforeApplication(t *testing.T) {
	h := grpcapi.Handler{RoutingAPI: routinggrpc.RoutingAPI{AllowRoutingTest: true}}
	for _, request := range []*routingv1.TestRouteRequest{nil, {}, {InstanceId: " "}, {InstanceId: "edge"}} {
		if result, err := h.TestRoute(verifiedContext(t), request); result != nil || status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid selection/context reached application: %v %v", result, err)
		} else {
			requireProbeOutcome(t, err, routingv1.RoutingProbeOutcome_ROUTING_PROBE_OUTCOME_NOT_EXECUTED)
		}
	}
	for _, input := range []*routingv1.RoutingContext{
		{Network: -1}, {Network: 255}, {Network: 257}, {SourcePort: 65536}, {TargetPort: math.MaxUint32}, {LocalPort: 65536}, {VlessRoute: 65536},
		{SourceIps: []string{"invalid"}}, {TargetIps: []string{"192.0.2.1", "invalid"}}, {LocalIps: []string{"fe80::1%eth0"}},
		{TargetIps: []string{" 192.0.2.1 "}}, {SourceIps: []string{"[2001:db8::1]"}},
	} {
		result, err := h.TestRoute(verifiedContext(t), &routingv1.TestRouteRequest{InstanceId: "edge", Context: input})
		if result != nil || status.Code(err) != codes.InvalidArgument {
			t.Fatalf("unrepresentable context reached application: %v %v", result, err)
		}
		requireProbeOutcome(t, err, routingv1.RoutingProbeOutcome_ROUTING_PROBE_OUTCOME_NOT_EXECUTED)
	}
}

func TestRoutePreservesFullContextAndConfirmedLateAcknowledgement(t *testing.T) {
	for _, network := range []struct {
		wire   routingv1.RoutingNetwork
		domain domain.RoutingNetwork
	}{
		{routingv1.RoutingNetwork_ROUTING_NETWORK_UNKNOWN, domain.RoutingUnknown}, {routingv1.RoutingNetwork_ROUTING_NETWORK_TCP, domain.RoutingTCP},
		{routingv1.RoutingNetwork_ROUTING_NETWORK_UDP, domain.RoutingUDP}, {routingv1.RoutingNetwork_ROUTING_NETWORK_UNIX, domain.RoutingUnix},
	} {
		ctx, cancel := context.WithCancel(verifiedContext(t))
		defer cancel()
		input := domain.RoutingContext{InboundTag: " exact>>>in ", Network: network.domain,
			SourceIPs: []netip.Addr{netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("::ffff:192.0.2.1"), netip.MustParseAddr("2001:db8::1")}, SourcePort: 65535,
			TargetIPs: []netip.Addr{netip.MustParseAddr("192.0.2.2")}, TargetPort: 0, TargetDomain: " Domain.Example ", Protocol: " opaque-protocol ", User: " Mixed@Example ",
			LocalIPs: []netip.Addr{netip.MustParseAddr("2001:db8::2")}, LocalPort: 1, VLESSRoute: 65535, Attributes: map[string]string{" key ": " value "}}
		request := &routingv1.TestRouteRequest{InstanceId: " edge ", Context: &routingv1.RoutingContext{
			InboundTag: input.InboundTag, Network: network.wire, SourceIps: []string{"2001:0db8:0:0::1", "::ffff:192.0.2.1", "2001:db8::1"}, SourcePort: 65535,
			TargetIps: []string{"192.0.2.2"}, TargetPort: 0, TargetDomain: input.TargetDomain, Protocol: input.Protocol, User: input.User, Attributes: input.Attributes,
			LocalIps: []string{"2001:db8::2"}, LocalPort: 1, VlessRoute: 65535,
		}, FieldSelectors: []string{"out", "unknown", "", "out"}, PublishResult: true}
		calls, ack := 0, time.UnixMilli(1700000000123)
		h := grpcapi.Handler{RoutingAPI: routinggrpc.RoutingAPI{AllowRoutingTest: true, Probes: routingProber(func(received context.Context, id instances.InstanceID, value domain.RoutingProbe) (domain.InstanceRoutingProbe, error) {
			calls++
			if received != ctx || id != " edge " || !reflect.DeepEqual(value.Context, input) || !reflect.DeepEqual(value.FieldSelectors, request.FieldSelectors) || !value.PublishResult {
				t.Fatal("probe identity/context/projection/publication changed")
			}
			cancel()
			return domain.InstanceRoutingProbe{InstanceID: id, Result: domain.RoutingProbeResult{Route: domain.RouteResult{Context: input, OutboundTag: " exact>>>out ", OutboundGroupTags: []string{"z", "a", "a", ""}}, AcknowledgedAt: ack}}, nil
		})}}
		response, err := h.TestRoute(ctx, request)
		if err != nil || calls != 1 || response.InstanceId != " edge " || response.AcknowledgedAtUnixMs != ack.UnixMilli() || response.Route.OutboundTag != " exact>>>out " || !reflect.DeepEqual(response.Route.OutboundGroupTags, []string{"z", "a", "a", ""}) {
			t.Fatalf("confirmed probe changed/disappeared: %v %v", response, err)
		}
		want := proto.Clone(request.Context).(*routingv1.RoutingContext)
		want.SourceIps[0] = "2001:db8::1"
		if !proto.Equal(response.Route.Context, want) {
			t.Fatalf("wire lost precision/order/mapped IPv6/opaque fields: %v", response)
		}
	}
}

func TestRouteAcceptsEmptyNativeContextAndNeverEchoesProjectedInput(t *testing.T) {
	for _, input := range []*routingv1.RoutingContext{{}, {TargetDomain: "request-only", User: "request-only", TargetPort: 443}} {
		h := grpcapi.Handler{RoutingAPI: routinggrpc.RoutingAPI{AllowRoutingTest: true, Probes: routingProber(func(context.Context, instances.InstanceID, domain.RoutingProbe) (domain.InstanceRoutingProbe, error) {
			return domain.InstanceRoutingProbe{InstanceID: "edge", Result: domain.RoutingProbeResult{AcknowledgedAt: time.Now()}}, nil
		})}}
		response, err := h.TestRoute(verifiedContext(t), &routingv1.TestRouteRequest{InstanceId: "edge", Context: input, FieldSelectors: []string{"unknown"}})
		if err != nil || response.Route == nil || response.Route.Context == nil || !proto.Equal(response.Route, &routingv1.RoutingResult{Context: &routingv1.RoutingContext{}}) {
			t.Fatalf("empty native projection was rejected/filled from input: %v %v", response, err)
		}
	}
}

func TestRouteFailureOutcomesAreExplicitAndDiagnosticsPrivate(t *testing.T) {
	for _, tc := range []struct {
		cause   error
		code    codes.Code
		unknown bool
	}{
		{instances.ErrNotFound, codes.NotFound, false}, {domain.ErrAPIDisabled, codes.FailedPrecondition, false},
		{domain.ErrRoutingProbeUnsupported, codes.FailedPrecondition, false}, {domain.ErrInvalidRoutingProbe, codes.InvalidArgument, false},
		{domain.ErrAPIBusy, codes.ResourceExhausted, false}, {grpc.ErrClientConnClosing, codes.Unavailable, false},
		{context.Canceled, codes.Canceled, false}, {context.DeadlineExceeded, codes.DeadlineExceeded, false},
		{domain.ErrRoutingProbeUnknown, codes.Unavailable, true}, {domain.ErrInvalidRoutingProbeResponse, codes.Unavailable, true},
		{errors.New("unexpected adapter failure"), codes.Unavailable, true}, {domain.ErrAPIResourceLimit, codes.ResourceExhausted, true},
		{errors.Join(domain.ErrRoutingProbeUnknown, context.Canceled), codes.Canceled, true},
		{errors.Join(domain.ErrRoutingProbeUnknown, context.DeadlineExceeded), codes.DeadlineExceeded, true},
		{errors.Join(domain.ErrRoutingProbeUnknown, domain.ErrAPIResourceLimit), codes.ResourceExhausted, true},
		{errors.Join(domain.ErrRoutingProbeUnknown, instances.ErrNotFound), codes.NotFound, true},
	} {
		h := grpcapi.Handler{RoutingAPI: routinggrpc.RoutingAPI{AllowRoutingTest: true, Probes: routingProber(func(context.Context, instances.InstanceID, domain.RoutingProbe) (domain.InstanceRoutingProbe, error) {
			return domain.InstanceRoutingProbe{Result: domain.RoutingProbeResult{AcknowledgedAt: time.Now()}}, errors.Join(tc.cause, errors.New("private endpoint/user/config"))
		})}}
		response, err := h.TestRoute(verifiedContext(t), &routingv1.TestRouteRequest{InstanceId: "edge", Context: &routingv1.RoutingContext{}})
		if response != nil || status.Code(err) != tc.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("failed probe exposed diagnostics/partial facts: %v %v", response, err)
		}
		want := routingv1.RoutingProbeOutcome_ROUTING_PROBE_OUTCOME_NOT_EXECUTED
		if tc.unknown {
			want = routingv1.RoutingProbeOutcome_ROUTING_PROBE_OUTCOME_UNKNOWN
		}
		requireProbeOutcome(t, err, want)
	}
}
