package services_test

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/core/routing/app/services"
	"github.com/PapilioVPN/node/internal/modules/core/routing/domain"
)

type routingProber func(context.Context, domain.RoutingProbe) (domain.RoutingProbeResult, error)

func (f routingProber) TestRoute(ctx context.Context, probe domain.RoutingProbe) (domain.RoutingProbeResult, error) {
	return f(ctx, probe)
}

func TestRoutingProbeSelectsOnlyExactInstanceAndRetainsAcknowledgedCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	probe := domain.RoutingProbe{Context: domain.RoutingContext{Network: domain.RoutingTCP, TargetIPs: []netip.Addr{netip.MustParseAddr("::ffff:192.0.2.1")}, TargetPort: 65535, User: " Mixed@Example "}, FieldSelectors: []string{"out", "ip_", "out"}, PublishResult: true}
	ack := domain.RoutingProbeResult{Route: domain.RouteResult{OutboundTag: " exact>>>out "}, AcknowledgedAt: time.Now()}
	calls := 0
	bindings := map[instances.InstanceID]services.RoutingProber{" edge ": routingProber(func(received context.Context, value domain.RoutingProbe) (domain.RoutingProbeResult, error) {
		calls++
		if received != ctx || !reflect.DeepEqual(value, probe) {
			t.Fatal("probe context/selection/input/projection/publication changed")
		}
		cancel()
		return ack, nil
	}), "disabled": nil}
	service := services.NewRoutingProbeService(bindings)
	delete(bindings, " edge ")
	result, err := service.TestRoute(ctx, " edge ", probe)
	if err != nil || result.InstanceID != " edge " || !reflect.DeepEqual(result.Result, ack) || calls != 1 {
		t.Fatalf("confirmed completion lost after cancellation: %+v %v", result, err)
	}
	if result, err := service.TestRoute(ctx, " edge ", probe); !errors.Is(err, context.Canceled) || errors.Is(err, domain.ErrRoutingProbeUnknown) || !reflect.DeepEqual(result, domain.InstanceRoutingProbe{}) || calls != 1 {
		t.Fatalf("pre-canceled probe reached effects: %+v %v", result, err)
	}
	for id, cause := range map[instances.InstanceID]error{"edge": instances.ErrNotFound, "disabled": domain.ErrAPIDisabled} {
		if _, err := service.TestRoute(t.Context(), id, probe); !errors.Is(err, cause) || errors.Is(err, domain.ErrRoutingProbeUnknown) || calls != 1 {
			t.Fatalf("probe crossed instance/feature boundary: %v", err)
		}
	}
}

func TestRoutingProbeFailuresPreserveCausesAndDiscardUnconfirmedFacts(t *testing.T) {
	for _, cause := range []error{domain.ErrRoutingProbeUnsupported, errors.Join(domain.ErrRoutingProbeUnknown, context.Canceled), errors.Join(domain.ErrRoutingProbeUnknown, domain.ErrAPIResourceLimit), errors.New("private diagnostics")} {
		calls := 0
		service := services.NewRoutingProbeService(map[instances.InstanceID]services.RoutingProber{"edge": routingProber(func(context.Context, domain.RoutingProbe) (domain.RoutingProbeResult, error) {
			calls++
			return domain.RoutingProbeResult{Route: domain.RouteResult{OutboundTag: "partial"}, AcknowledgedAt: time.Now()}, cause
		})})
		result, err := service.TestRoute(t.Context(), "edge", domain.RoutingProbe{})
		if !errors.Is(err, cause) || !reflect.DeepEqual(result, domain.InstanceRoutingProbe{}) || calls != 1 {
			t.Fatalf("failed/uncertain probe became acknowledged or replayed: %+v %v", result, err)
		}
	}
}
