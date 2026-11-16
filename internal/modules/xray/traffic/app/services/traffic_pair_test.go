package services_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/traffic/app/services"
	"github.com/PapilioVPN/node/internal/modules/xray/traffic/domain"
)

func TestTrafficPairReadsTwoExactCountersAndPreservesIndependentAbsence(t *testing.T) {
	for _, presence := range []int{0, 1, 2, 3} {
		calls := 0
		var deadline time.Time
		observed := time.Unix(1700000000, 0)
		selection := domain.TrafficPairSelection{Scope: domain.UserTraffic, Name: " exact>>>opaque "}
		bindings := map[instances.InstanceID]services.TrafficPairReadBinding{
			" edge ": {Timeout: time.Second, Reader: trafficReader(func(ctx context.Context, query domain.TrafficSelection) (domain.TrafficCounter, error) {
				if query.Scope != selection.Scope || query.Name != selection.Name || query.Direction != [2]domain.TrafficDirection{domain.Uplink, domain.Downlink}[calls] {
					t.Fatalf("selection: %+v", query)
				}
				actual, ok := ctx.Deadline()
				if !ok {
					t.Fatal("no total deadline")
				}
				if calls == 0 {
					deadline = actual
				} else if !deadline.Equal(actual) {
					t.Fatal("deadline extended")
				}
				calls++
				if presence&(1<<(calls-1)) == 0 {
					return domain.TrafficCounter{}, domain.ErrTrafficCounterNotFound
				}
				return domain.TrafficCounter{Bytes: [2]int64{0, math.MinInt64}[calls-1], ObservedAt: observed.Add(time.Duration(calls) * time.Second)}, nil
			})},
		}
		service := services.NewTrafficPairService(bindings)
		delete(bindings, " edge ")
		result, err := service.GetTrafficPair(t.Context(), " edge ", selection)
		if err != nil || calls != 2 || result.InstanceID != " edge " || result.Selection != selection || (result.Pair.Uplink != nil) != (presence&1 != 0) || (result.Pair.Downlink != nil) != (presence&2 != 0) {
			t.Fatalf("presence %d: %+v %v calls=%d", presence, result, err, calls)
		}
		if presence&1 != 0 && (result.Pair.Uplink.Bytes != 0 || result.Pair.Uplink.ObservedAt != observed.Add(time.Second)) {
			t.Fatal("zero uplink became absence")
		}
		if presence&2 != 0 && (result.Pair.Downlink.Bytes != math.MinInt64 || result.Pair.Downlink.ObservedAt != observed.Add(2*time.Second)) {
			t.Fatal("downlink precision/time lost")
		}
	}
}

func TestTrafficPairDiscardsPartialReadsAndPreservesFailureAndCancellation(t *testing.T) {
	for _, mode := range []string{"failure", "cancel", "deadline"} {
		ctx, cancel := context.WithCancel(t.Context())
		calls := 0
		failure := domain.ErrAPIResourceLimit
		timeout := time.Second
		if mode == "deadline" {
			timeout, failure = 10*time.Millisecond, context.DeadlineExceeded
		}
		service := services.NewTrafficPairService(map[instances.InstanceID]services.TrafficPairReadBinding{"edge": {Timeout: timeout, Reader: trafficReader(func(ctx context.Context, _ domain.TrafficSelection) (domain.TrafficCounter, error) {
			calls++
			if mode == "cancel" {
				cancel()
			}
			if mode == "deadline" {
				<-ctx.Done()
				return domain.TrafficCounter{}, ctx.Err()
			}
			if calls == 2 {
				return domain.TrafficCounter{}, failure
			}
			return domain.TrafficCounter{Bytes: 99}, nil
		})}})
		result, err := service.GetTrafficPair(ctx, "edge", domain.TrafficPairSelection{Scope: domain.InboundTraffic, Name: "proxy"})
		cancel()
		if mode == "cancel" {
			failure = context.Canceled
		}
		if !errors.Is(err, failure) || result != (domain.InstanceTrafficPair{}) || mode == "failure" && calls != 2 || mode != "failure" && calls != 1 {
			t.Fatalf("%s read retained partial facts: %+v %v calls=%d", mode, result, err, calls)
		}
	}
}

func TestTrafficPairRejectsMissingDisabledAndCanceledSelectionWithoutCallingNative(t *testing.T) {
	service := services.NewTrafficPairService(map[instances.InstanceID]services.TrafficPairReadBinding{"disabled": {}})
	for id, cause := range map[instances.InstanceID]error{"missing": instances.ErrNotFound, "disabled": domain.ErrAPIDisabled} {
		if _, err := service.GetTrafficPair(t.Context(), id, domain.TrafficPairSelection{}); !errors.Is(err, cause) {
			t.Fatalf("%q: %v", id, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.GetTrafficPair(ctx, "missing", domain.TrafficPairSelection{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
