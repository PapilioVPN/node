//go:build integration

package api_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	native "github.com/xtls/xray-core/app/router/command"
	netnative "github.com/xtls/xray-core/common/net"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	routingdomain "github.com/PapilioVPN/node/internal/modules/core/routing/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

type routingProbeServer struct {
	native.UnimplementedRoutingServiceServer
	probe func(context.Context, *native.TestRouteRequest) (*native.RoutingContext, error)
}

func TestRoutingProbePreservesAllContextFieldsWithOneCallAtEverySize(t *testing.T) {
	for _, size := range []int{0, 1, 100, 1000} {
		for _, network := range []struct {
			domain routingdomain.RoutingNetwork
			native netnative.Network
		}{
			{routingdomain.RoutingUnknown, netnative.Network_Unknown}, {routingdomain.RoutingTCP, netnative.Network_TCP},
			{routingdomain.RoutingUDP, netnative.Network_UDP}, {routingdomain.RoutingUnix, netnative.Network_UNIX},
		} {
			t.Run(fmt.Sprintf("%d/%s", size, network.native), func(t *testing.T) {
				input := routingdomain.RoutingContext{InboundTag: " exact>>>in ", Network: network.domain,
					SourcePort: 65535, TargetPort: 0, LocalPort: 1, VLESSRoute: 65535,
					TargetDomain: " Domain.Example ", Protocol: " opaque-protocol ", User: " Mixed@Example "}
				if size > 0 {
					input.Attributes = make(map[string]string, size)
				}
				expected := &native.RoutingContext{InboundTag: input.InboundTag, Network: network.native, SourcePort: 65535, TargetPort: 0, LocalPort: 1, VlessRoute: 65535,
					TargetDomain: input.TargetDomain, Protocol: input.Protocol, User: input.User, Attributes: input.Attributes}
				for i := 0; i < size; i++ {
					ip := netip.MustParseAddr([]string{"192.0.2.1", "2001:db8::1", "::ffff:192.0.2.1"}[i%3])
					input.SourceIPs = append(input.SourceIPs, ip)
					input.TargetIPs = append(input.TargetIPs, ip)
					input.LocalIPs = append(input.LocalIPs, ip)
					expected.SourceIPs = append(expected.SourceIPs, ip.AsSlice())
					expected.TargetIPs = append(expected.TargetIPs, ip.AsSlice())
					expected.LocalIPs = append(expected.LocalIPs, ip.AsSlice())
					input.Attributes[fmt.Sprint(" key ", i)] = fmt.Sprint(" value ", i)
				}
				selectors := []string{"out", "ip_", "unknown", "", "out"}
				address, counts := routingAPI(t, routingProbeServer{probe: func(_ context.Context, r *native.TestRouteRequest) (*native.RoutingContext, error) {
					if !proto.Equal(r.RoutingContext, expected) || !reflect.DeepEqual(r.FieldSelectors, selectors) || !r.PublishResult {
						t.Error("native request narrowed, normalized or changed context/projection/publication")
					}
					reply := proto.Clone(r.RoutingContext).(*native.RoutingContext)
					reply.OutboundTag, reply.OutboundGroupTags = " exact>>>out ", []string{"z", "a", "a", ""}
					return reply, nil
				}})
				client := apiClient(t, address, time.Second, 512*1024)
				probe := routingdomain.RoutingProbe{Context: input, FieldSelectors: selectors, PublishResult: true}
				result, err := client.TestRoute(t.Context(), probe)
				if err != nil || !reflect.DeepEqual(result.Route.Context, input) || result.Route.OutboundTag != " exact>>>out " || !reflect.DeepEqual(result.Route.OutboundGroupTags, []string{"z", "a", "a", ""}) || result.AcknowledgedAt.IsZero() || counts.rpcs.Load() != 1 || counts.opened.Load() != 1 {
					t.Fatalf("native full context changed or probe over-fetched: %v calls=%d channels=%d", err, counts.rpcs.Load(), counts.opened.Load())
				}
				if size > 0 {
					result.Route.Context.Attributes[" key 0"] = "caller-owned"
					result.Route.Context.SourceIPs[0] = netip.MustParseAddr("192.0.2.2")
					result.Route.OutboundGroupTags[0] = "caller-owned"
					again, err := client.TestRoute(t.Context(), probe)
					if err != nil || !reflect.DeepEqual(again.Route.Context, input) || again.Route.OutboundGroupTags[0] != "z" {
						t.Fatal("caller result aliased input/native/cache state")
					}
				}
			})
		}
	}
}

func TestRoutingProbeFailuresRetainUnknownEffectsAndNeverRetry(t *testing.T) {
	for _, code := range []codes.Code{codes.Unimplemented, codes.NotFound, codes.InvalidArgument, codes.Unknown, codes.Unavailable, codes.ResourceExhausted, codes.Canceled, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			address, counts := routingAPI(t, routingProbeServer{probe: func(context.Context, *native.TestRouteRequest) (*native.RoutingContext, error) {
				return nil, status.Error(code, "private diagnostic")
			}})
			result, err := apiClient(t, address, time.Second, 4096).TestRoute(t.Context(), routingdomain.RoutingProbe{})
			if err == nil || !reflect.DeepEqual(result, routingdomain.RoutingProbeResult{}) || status.Code(err) != code || counts.rpcs.Load() != 1 || errors.Is(err, routingdomain.ErrRoutingProbeUnknown) != (code != codes.Unimplemented) {
				t.Fatalf("probe failure lost causes/outcome or retried: %+v %v calls=%d", result, err, counts.rpcs.Load())
			}
			if code == codes.Unimplemented && !errors.Is(err, routingdomain.ErrRoutingProbeUnsupported) {
				t.Fatal(err)
			}
			if code == codes.ResourceExhausted && !errors.Is(err, domain.ErrAPIResourceLimit) {
				t.Fatal(err)
			}
			if code == codes.Canceled && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if code == codes.DeadlineExceeded && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
		})
	}
}

func TestRoutingProbeRejectsInvalidRepresentationsBeforeNativeEffects(t *testing.T) {
	for _, probe := range []routingdomain.RoutingProbe{
		{Context: routingdomain.RoutingContext{Network: 255}},
		{Context: routingdomain.RoutingContext{InboundTag: "\xff"}}, {Context: routingdomain.RoutingContext{User: "\xff"}},
		{Context: routingdomain.RoutingContext{TargetDomain: "\xff"}}, {Context: routingdomain.RoutingContext{Protocol: "\xff"}},
		{Context: routingdomain.RoutingContext{Attributes: map[string]string{"\xff": "value"}}},
		{Context: routingdomain.RoutingContext{Attributes: map[string]string{"key": "\xff"}}}, {FieldSelectors: []string{"\xff"}},
		{Context: routingdomain.RoutingContext{SourceIPs: []netip.Addr{{}}}}, {Context: routingdomain.RoutingContext{TargetIPs: []netip.Addr{{}}}},
		{Context: routingdomain.RoutingContext{LocalIPs: []netip.Addr{netip.MustParseAddr("fe80::1%eth0")}}},
	} {
		address, counts := routingAPI(t, routingProbeServer{})
		result, err := apiClient(t, address, time.Second, 4096).TestRoute(t.Context(), probe)
		if !errors.Is(err, routingdomain.ErrInvalidRoutingProbe) || errors.Is(err, routingdomain.ErrRoutingProbeUnknown) || !reflect.DeepEqual(result, routingdomain.RoutingProbeResult{}) || counts.rpcs.Load() != 0 || counts.opened.Load() != 0 {
			t.Fatalf("invalid probe reached native workers/effects: %+v %v", result, err)
		}
	}
}

func TestRoutingProbeMalformedOrOversizedReplyRetainsPossibleEffects(t *testing.T) {
	unknown := &native.RoutingContext{OutboundTag: "out"}
	unknown.ProtoReflect().SetUnknown(protowire.AppendVarint(protowire.AppendTag(nil, 99, protowire.VarintType), 1))
	for _, reply := range []*native.RoutingContext{
		{Network: 99}, {SourcePort: 65536}, {TargetPort: 65536}, {LocalPort: 65536}, {VlessRoute: 65536},
		{SourceIPs: [][]byte{{1}}}, {TargetIPs: [][]byte{{1, 2, 3}}}, {LocalIPs: [][]byte{nil}}, unknown,
	} {
		address, counts := routingAPI(t, routingProbeServer{probe: func(context.Context, *native.TestRouteRequest) (*native.RoutingContext, error) { return reply, nil }})
		result, err := apiClient(t, address, time.Second, 4096).TestRoute(t.Context(), routingdomain.RoutingProbe{})
		if !errors.Is(err, routingdomain.ErrInvalidRoutingProbeResponse) || !errors.Is(err, routingdomain.ErrRoutingProbeUnknown) || !reflect.DeepEqual(result, routingdomain.RoutingProbeResult{}) || counts.rpcs.Load() != 1 {
			t.Fatalf("malformed reply became successful facts/no-effect: %+v %v", result, err)
		}
	}
	address, counts := routingAPI(t, routingProbeServer{probe: func(context.Context, *native.TestRouteRequest) (*native.RoutingContext, error) {
		return &native.RoutingContext{OutboundTag: "large"}, nil
	}})
	result, err := apiClient(t, address, time.Second, 1).TestRoute(t.Context(), routingdomain.RoutingProbe{})
	if !errors.Is(err, domain.ErrAPIResourceLimit) || !errors.Is(err, routingdomain.ErrRoutingProbeUnknown) || !reflect.DeepEqual(result, routingdomain.RoutingProbeResult{}) || counts.rpcs.Load() != 1 {
		t.Fatalf("oversized reply lost possible effects: %+v %v", result, err)
	}
	address, _ = routingAPI(t, routingProbeServer{probe: func(context.Context, *native.TestRouteRequest) (*native.RoutingContext, error) {
		return &native.RoutingContext{}, nil
	}})
	result, err = apiClient(t, address, time.Second, 4096).TestRoute(t.Context(), routingdomain.RoutingProbe{FieldSelectors: []string{"unknown"}})
	if err != nil || result.AcknowledgedAt.IsZero() || !reflect.DeepEqual(result.Route, routingdomain.RouteResult{}) {
		t.Fatalf("valid empty projection became failure: %+v %v", result, err)
	}
}

func TestRoutingProbeCanceledAfterEffectSharesCapacityAndNeverReplays(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "close"} {
		t.Run(mode, func(t *testing.T) {
			var effects atomic.Int32
			entered := make(chan struct{})
			address, counts := routingAPI(t, routingProbeServer{probe: func(ctx context.Context, _ *native.TestRouteRequest) (*native.RoutingContext, error) {
				effects.Add(1)
				close(entered)
				<-ctx.Done()
				return nil, status.FromContextError(ctx.Err()).Err()
			}})
			client := apiClient(t, address, time.Second, 4096)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			var probeError error
			go func() { defer close(done); _, probeError = client.TestRoute(ctx, routingdomain.RoutingProbe{}) }()
			defer func() { cancel(); _ = client.Close(); <-done }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("native probe did not start")
			}
			if _, err := client.TestRoute(t.Context(), routingdomain.RoutingProbe{}); !errors.Is(err, domain.ErrAPIBusy) || errors.Is(err, routingdomain.ErrRoutingProbeUnknown) {
				t.Fatalf("saturated probe bypassed admission or claimed possible effects: %v", err)
			}
			if _, err := client.ReadRuntimeInfo(t.Context()); !errors.Is(err, domain.ErrAPIBusy) {
				t.Fatalf("runtime bypassed probe capacity: %v", err)
			}
			if _, err := client.ReadRoutingRules(t.Context()); !errors.Is(err, domain.ErrAPIBusy) {
				t.Fatalf("routing read bypassed probe capacity: %v", err)
			}
			switch mode {
			case "cancel":
				cancel()
			case "close":
				if err := client.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-done:
				err := probeError
				if !errors.Is(err, routingdomain.ErrRoutingProbeUnknown) || effects.Load() != 1 || counts.rpcs.Load() != 1 {
					t.Fatalf("lost response erased/replayed effects: %v effects=%d calls=%d", err, effects.Load(), counts.rpcs.Load())
				}
				if mode == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if mode == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("native probe failed to join after cancellation/close")
			}
		})
	}
}

func (s routingProbeServer) TestRoute(ctx context.Context, r *native.TestRouteRequest) (*native.RoutingContext, error) {
	return s.probe(ctx, r)
}

func TestRoutingProbeDispatchesOneNativeCommandOnExistingChannel(t *testing.T) {
	address, counts := routingAPI(t, routingProbeServer{probe: func(_ context.Context, r *native.TestRouteRequest) (*native.RoutingContext, error) {
		if r.GetRoutingContext().GetTargetDomain() != " opaque.example " || !r.PublishResult || len(r.FieldSelectors) != 1 || r.FieldSelectors[0] != "out" {
			t.Error("native probe input/projection/publication changed")
		}
		return &native.RoutingContext{OutboundTag: " exact>>>out "}, nil
	}})
	client := apiClient(t, address, time.Second, 4096)
	if _, err := client.ReadRuntimeInfo(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	result, err := client.TestRoute(t.Context(), routingdomain.RoutingProbe{Context: routingdomain.RoutingContext{TargetDomain: " opaque.example "}, FieldSelectors: []string{"out"}, PublishResult: true})
	if err != nil || result.Route.OutboundTag != " exact>>>out " || result.AcknowledgedAt.Before(before) || result.AcknowledgedAt.After(time.Now()) || counts.rpcs.Load() != 2 || counts.opened.Load() != 1 {
		t.Fatalf("native probe was missing/changed or added calls/channels: %+v %v calls=%d channels=%d", result, err, counts.rpcs.Load(), counts.opened.Load())
	}
}
