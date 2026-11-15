package api

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	native "github.com/xtls/xray-core/app/router/command"
	"google.golang.org/grpc"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

type completingRoutingClient struct {
	native.RoutingServiceClient
	complete func() (*native.RoutingContext, error)
}

func (c completingRoutingClient) TestRoute(context.Context, *native.TestRouteRequest, ...grpc.CallOption) (*native.RoutingContext, error) {
	return c.complete()
}

// A controlled SDK completion makes this race deterministic. Wire cancellation
// and uncertain remote effects are covered separately by native-service tests.
func TestRoutingProbeRetainsAcknowledgementOrFailureCauseAfterLateCancellation(t *testing.T) {
	for _, valid := range []bool{false, true} {
		client, err := NewClient(Config{Network: "unix", Address: "/unused/api.sock", RequestTimeout: time.Second, MaxReceiveBytes: 4096, MaxConcurrent: 1})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := client.Close(); err != nil {
				t.Error(err)
			}
		})
		if _, err := client.routingClient(); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		client.routing = completingRoutingClient{complete: func() (*native.RoutingContext, error) {
			cancel()
			if valid {
				return &native.RoutingContext{OutboundTag: "confirmed"}, nil
			}
			return nil, nil
		}}
		result, err := client.TestRoute(ctx, domain.RoutingProbe{})
		if valid {
			if err != nil || result.Route.OutboundTag != "confirmed" || result.AcknowledgedAt.IsZero() {
				t.Fatalf("late cancellation erased native acknowledgement: %+v %v", result, err)
			}
		} else if !errors.Is(err, domain.ErrInvalidRoutingProbeResponse) || !errors.Is(err, domain.ErrRoutingProbeUnknown) || !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, domain.RoutingProbeResult{}) {
			t.Fatalf("invalid reply lost native uncertainty/cancellation or returned facts: %+v %v", result, err)
		}
	}
}
