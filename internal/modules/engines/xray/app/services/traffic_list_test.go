package services_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/app/services"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

type trafficListReader func(context.Context, domain.TrafficScope) (domain.TrafficCounters, error)

func (f trafficListReader) ReadTrafficCounters(ctx context.Context, scope domain.TrafficScope) (domain.TrafficCounters, error) {
	return f(ctx, scope)
}

func TestTrafficCollectionSelectsOneBindingAndOrdersWithoutLosingDuplicates(t *testing.T) {
	observed := time.Now()
	calls := 0
	ctx := t.Context()
	bindings := map[instances.InstanceID]services.TrafficCountersReader{
		" edge ": trafficListReader(func(received context.Context, scope domain.TrafficScope) (domain.TrafficCounters, error) {
			calls++
			if received != ctx || scope != domain.UserTraffic {
				t.Fatal("changed context/scope")
			}
			return domain.TrafficCounters{ObservedAt: observed, Items: []domain.TrafficCounterEntry{
				{Name: "z", Direction: domain.Uplink, Bytes: 99},
				{Name: "a", Direction: domain.Uplink, Bytes: math.MinInt64},
				{Name: "a", Direction: domain.Downlink, Bytes: math.MaxInt64},
				{Name: "a", Direction: domain.Downlink, Bytes: 0},
			}}, nil
		}),
		"disabled": nil,
		"other": trafficListReader(func(context.Context, domain.TrafficScope) (domain.TrafficCounters, error) {
			t.Fatal("unselected API called")
			return domain.TrafficCounters{}, nil
		}),
	}
	service := services.NewTrafficCountersService(bindings)
	delete(bindings, " edge ")
	result, err := service.ListTrafficCounters(ctx, " edge ", domain.UserTraffic)
	want := []domain.TrafficCounterEntry{
		{Name: "a", Direction: domain.Downlink, Bytes: math.MaxInt64},
		{Name: "a", Direction: domain.Downlink, Bytes: 0},
		{Name: "a", Direction: domain.Uplink, Bytes: math.MinInt64},
		{Name: "z", Direction: domain.Uplink, Bytes: 99},
	}
	if err != nil || calls != 1 || result.InstanceID != " edge " || result.Scope != domain.UserTraffic || result.Counters.ObservedAt != observed || !reflect.DeepEqual(result.Counters.Items, want) {
		t.Fatalf("collection: %+v, %v; calls=%d", result, err, calls)
	}
	for id, cause := range map[instances.InstanceID]error{"edge": instances.ErrNotFound, "missing": instances.ErrNotFound, "disabled": domain.ErrAPIDisabled} {
		result, err := service.ListTrafficCounters(ctx, id, domain.UserTraffic)
		if !errors.Is(err, cause) || result.InstanceID != "" || result.Counters.Items != nil {
			t.Fatalf("selection %q: %+v, %v", id, result, err)
		}
	}
	if calls != 1 {
		t.Fatal("selection failure called native API")
	}
}

func TestTrafficCollectionDiscardsFailedCanceledAndEmptyObservations(t *testing.T) {
	for _, mode := range []string{"empty", "failure", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			service := services.NewTrafficCountersService(map[instances.InstanceID]services.TrafficCountersReader{
				"edge": trafficListReader(func(context.Context, domain.TrafficScope) (domain.TrafficCounters, error) {
					calls++
					if mode == "empty" {
						return domain.TrafficCounters{ObservedAt: time.Now()}, nil
					}
					if mode == "cancel" {
						cancel()
					}
					return domain.TrafficCounters{Items: []domain.TrafficCounterEntry{{Bytes: 99}}}, domain.ErrAPIResourceLimit
				}),
			})
			result, err := service.ListTrafficCounters(ctx, "edge", domain.UserTraffic)
			if mode == "empty" {
				if err != nil || result.InstanceID != "edge" || len(result.Counters.Items) != 0 || result.Counters.ObservedAt.IsZero() {
					t.Fatalf("empty scope: %+v, %v", result, err)
				}
				return
			}
			if result.InstanceID != "" || result.Counters.Items != nil || !errors.Is(err, domain.ErrAPIResourceLimit) || mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("failed/canceled facts: %+v, %v", result, err)
			}
			cancel()
			if _, err := service.ListTrafficCounters(ctx, "edge", domain.UserTraffic); !errors.Is(err, context.Canceled) || calls != 1 {
				t.Fatalf("canceled admission: %v, calls=%d", err, calls)
			}
		})
	}
}
