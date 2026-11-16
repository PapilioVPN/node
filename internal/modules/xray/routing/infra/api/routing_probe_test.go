package api

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	native "github.com/xtls/xray-core/app/router/command"
	"google.golang.org/grpc"

	"github.com/PapilioVPN/node/internal/modules/xray/routing/domain"
)

type completingRoutingClient struct {
	native.RoutingServiceClient
	complete func() (*native.RoutingContext, error)
}

// Controlled native port for routing acknowledgement races; native tests
// separately cover channel ownership and cancellation over the wire.
type completingRoutingAPI struct {
	client native.RoutingServiceClient
}

func (a completingRoutingAPI) Begin(ctx context.Context) (context.Context, func(), error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	return ctx, cancel, nil
}

func (a completingRoutingAPI) RoutingClient() (native.RoutingServiceClient, error) {
	return a.client, nil
}

func (c completingRoutingClient) TestRoute(context.Context, *native.TestRouteRequest, ...grpc.CallOption) (*native.RoutingContext, error) {
	return c.complete()
}

// A controlled SDK completion makes this race deterministic. Wire cancellation
// and uncertain remote effects are covered separately by native-service tests.
func TestRoutingProbeRetainsAcknowledgementOrFailureCauseAfterLateCancellation(t *testing.T) {
	for _, valid := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		client := &Router{API: completingRoutingAPI{client: completingRoutingClient{complete: func() (*native.RoutingContext, error) {
			cancel()
			if valid {
				return &native.RoutingContext{OutboundTag: "confirmed"}, nil
			}
			return nil, nil
		}}}}
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
