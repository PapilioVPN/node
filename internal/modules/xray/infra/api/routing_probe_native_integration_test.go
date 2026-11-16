//go:build integration

package api_test

import (
	"reflect"
	"testing"
	"time"

	routernative "github.com/xtls/xray-core/app/router"
	native "github.com/xtls/xray-core/app/router/command"
	statsnative "github.com/xtls/xray-core/app/stats"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/features/stats"

	"github.com/PapilioVPN/node/internal/modules/xray/routing/domain"
)

// The official SDK service can be constructed with a routing-stat channel.
// The pinned standalone binary instead registers that service with a nil channel;
// this test does not claim that binary publishes routing statistics.
func TestRoutingProbeNativeProjectionAndOptionalPublication(t *testing.T) {
	router := new(routernative.Router)
	if err := router.Init(t.Context(), &routernative.Config{Rule: []*routernative.RoutingRule{{
		InboundTag: []string{"route-in"}, TargetTag: &routernative.RoutingRule_Tag{Tag: "route-out"},
	}}}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := router.Close(); err != nil {
			t.Error(err)
		}
	})
	channel := statsnative.NewChannel(&statsnative.ChannelConfig{SubscriberLimit: 1, BufferSize: 1, Blocking: true})
	subscriber, err := stats.SubscribeRunnableChannel(channel)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := channel.Close(); err != nil {
			t.Error(err)
		}
		// Native Close signals the SDK worker. The closed subscription observes
		// its completed channel cleanup; no test-owned consumer is detached.
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		for {
			select {
			case _, open := <-subscriber:
				if !open {
					return
				}
			case <-timer.C:
				t.Error("native routing-stat worker failed to close")
				return
			}
		}
	})
	address, counts := routingAPI(t, native.NewRoutingServer(router, channel))
	client := apiClient(t, address, time.Second, 4096)
	for _, publish := range []bool{false, true} {
		user := "not-published"
		if publish {
			user = "published"
		}
		result, err := client.TestRoute(t.Context(), domain.RoutingProbe{
			Context:        domain.RoutingContext{InboundTag: "route-in", User: user, Network: domain.RoutingTCP},
			FieldSelectors: []string{"out"}, PublishResult: publish,
		})
		if err != nil || result.Route.OutboundTag != "route-out" || !reflect.DeepEqual(result.Route.Context, domain.RoutingContext{}) || result.AcknowledgedAt.IsZero() {
			t.Fatalf("native projection failed: %+v %v", result, err)
		}
	}
	select {
	case event := <-subscriber:
		route, ok := event.(routing.Route)
		if !ok || route.GetUser() != "published" || route.GetInboundTag() != "route-in" || route.GetOutboundTag() != "route-out" {
			t.Fatalf("publication flag/actual native event changed: %v", event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("configured SDK service did not publish the probe")
	}
	if counts.rpcs.Load() != 2 || counts.opened.Load() != 1 {
		t.Fatal("native probe added requests/connections")
	}
}
