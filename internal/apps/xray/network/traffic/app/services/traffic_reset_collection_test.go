package services_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/network/traffic/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/network/traffic/domain"
)

func resetCollectionService(reader trafficListReader, resetter trafficResetter, timeout time.Duration) *services.TrafficCountersResetService {
	return services.NewTrafficCountersResetService(map[instances.InstanceID]services.TrafficResetBinding{
		"edge": {Reader: reader, Resetter: resetter, Timeout: timeout},
	})
}

func TestScopeResetPreflightsThenResetsExactSelectionsAtBoundedCallGrowth(t *testing.T) {
	for _, size := range []int{0, 1, 100, 1000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			reads, resets := 0, 0
			selectedAt, acknowledgedAt := time.Now(), time.Now().Add(time.Second)
			service := resetCollectionService(func(_ context.Context, scope domain.TrafficScope) (domain.TrafficCounters, error) {
				reads++
				if scope != domain.UserTraffic {
					t.Fatal("scope changed")
				}
				items := make([]domain.TrafficCounterEntry, size)
				for i := range items {
					items[i] = domain.TrafficCounterEntry{Name: fmt.Sprintf("user-%04d>>>opaque", size-1-i), Direction: domain.Downlink, Bytes: 999}
				}
				return domain.TrafficCounters{Items: items, ObservedAt: selectedAt}, nil
			}, func(ctx context.Context, selection domain.TrafficSelection) (domain.TrafficCounterReset, error) {
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("collection has no total deadline")
				}
				if selection != (domain.TrafficSelection{Scope: domain.UserTraffic, Name: fmt.Sprintf("user-%04d>>>opaque", resets), Direction: domain.Downlink}) {
					t.Fatalf("selection/order changed: %+v", selection)
				}
				resets++
				return domain.TrafficCounterReset{PreviousBytes: math.MinInt64, AcknowledgedAt: acknowledgedAt}, nil
			}, time.Second)
			result, err := service.ResetTrafficCounters(t.Context(), "edge", domain.UserTraffic, max(1, size))
			if err != nil || reads != 1 || resets != size || result.InstanceID != "edge" || result.Scope != domain.UserTraffic || result.Reset.SelectedAt != selectedAt || len(result.Reset.Items) != size {
				t.Fatalf("reset: %+v %v calls=%d+%d", result, err, reads, resets)
			}
			for _, item := range result.Reset.Items {
				if item.State != domain.TrafficResetAcknowledged || item.Reset == nil || item.Reset.PreviousBytes != math.MinInt64 || item.Reset.AcknowledgedAt != acknowledgedAt {
					t.Fatalf("lost acknowledgement: %+v", item)
				}
			}
		})
	}
}

func TestScopeResetRejectsOversizeDuplicateAndFailedDiscoveryBeforeEffects(t *testing.T) {
	for _, test := range []struct {
		name          string
		items         []domain.TrafficCounterEntry
		failure, want error
		limit         int
	}{
		{"over-limit", []domain.TrafficCounterEntry{{Name: "a", Direction: domain.Uplink}, {Name: "b", Direction: domain.Uplink}}, nil, domain.ErrAPIResourceLimit, 1},
		{"duplicate", []domain.TrafficCounterEntry{{Name: "a", Direction: domain.Uplink}, {Name: "a", Direction: domain.Uplink}}, nil, domain.ErrInvalidTrafficResponse, 2},
		{"failed-read", []domain.TrafficCounterEntry{{Name: "a", Direction: domain.Uplink}}, domain.ErrTrafficUnsupported, domain.ErrTrafficUnsupported, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := resetCollectionService(func(context.Context, domain.TrafficScope) (domain.TrafficCounters, error) {
				return domain.TrafficCounters{Items: test.items}, test.failure
			}, func(context.Context, domain.TrafficSelection) (domain.TrafficCounterReset, error) {
				t.Fatal("preflight failure changed engine")
				return domain.TrafficCounterReset{}, nil
			}, time.Second)
			result, err := service.ResetTrafficCounters(t.Context(), "edge", domain.UserTraffic, test.limit)
			if !errors.Is(err, test.want) || result.Reset.Items != nil {
				t.Fatalf("preflight: %+v %v", result, err)
			}
		})
	}
}

func TestScopeResetStopsOnFirstFailureAndRetainsExactProgress(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprint(unknown), func(t *testing.T) {
			failure := domain.ErrTrafficCounterNotFound
			state := domain.TrafficResetNotApplied
			if unknown {
				failure = errors.Join(domain.ErrTrafficResetUnknown, context.DeadlineExceeded)
				state = domain.TrafficResetUnknown
			}
			calls := 0
			service := resetCollectionService(func(context.Context, domain.TrafficScope) (domain.TrafficCounters, error) {
				return domain.TrafficCounters{Items: []domain.TrafficCounterEntry{{Name: "a", Direction: domain.Uplink}, {Name: "b", Direction: domain.Downlink}, {Name: "c", Direction: domain.Uplink}}, ObservedAt: time.Now()}, nil
			}, func(context.Context, domain.TrafficSelection) (domain.TrafficCounterReset, error) {
				calls++
				if calls == 2 {
					return domain.TrafficCounterReset{PreviousBytes: 999}, failure
				}
				return domain.TrafficCounterReset{PreviousBytes: math.MaxInt64, AcknowledgedAt: time.Now()}, nil
			}, time.Second)
			result, err := service.ResetTrafficCounters(t.Context(), "edge", domain.UserTraffic, 3)
			if !errors.Is(err, failure) || calls != 2 || len(result.Reset.Items) != 3 {
				t.Fatalf("failed reset: %+v %v calls=%d", result, err, calls)
			}
			items := result.Reset.Items
			if items[0].State != domain.TrafficResetAcknowledged || items[0].Reset == nil || items[0].Reset.PreviousBytes != math.MaxInt64 || items[1].State != state || items[1].Reset != nil || items[2].State != domain.TrafficResetNotAttempted || items[2].Reset != nil {
				t.Fatalf("partial effects lost: %+v", items)
			}
		})
	}
}

func TestScopeResetRetainsAcknowledgementBeforeCancellationAndNeverExtendsBudget(t *testing.T) {
	for _, final := range []bool{false, true} {
		t.Run(fmt.Sprint(final), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var deadline time.Time
			calls := 0
			service := resetCollectionService(func(ctx context.Context, domainScope domain.TrafficScope) (domain.TrafficCounters, error) {
				deadline, _ = ctx.Deadline()
				items := []domain.TrafficCounterEntry{{Name: " exact>>>opaque ", Direction: domain.Uplink}}
				if !final {
					items = append(items, domain.TrafficCounterEntry{Name: "z", Direction: domain.Downlink})
				}
				return domain.TrafficCounters{Items: items}, nil
			}, func(ctx context.Context, selection domain.TrafficSelection) (domain.TrafficCounterReset, error) {
				calls++
				actual, _ := ctx.Deadline()
				if !actual.Equal(deadline) {
					t.Fatal("reset extended discovery deadline")
				}
				cancel()
				return domain.TrafficCounterReset{PreviousBytes: 0, AcknowledgedAt: time.Now()}, nil
			}, time.Second)
			result, err := service.ResetTrafficCounters(ctx, "edge", domain.UserTraffic, 2)
			if final && err != nil || !final && !errors.Is(err, context.Canceled) || calls != 1 || result.Reset.Items[0].State != domain.TrafficResetAcknowledged || result.Reset.Items[0].Reset == nil || !final && result.Reset.Items[1].State != domain.TrafficResetNotAttempted {
				t.Fatalf("late cancellation: %+v %v", result, err)
			}
		})
	}
}

func TestScopeResetSelectionAndCanceledAdmissionHaveNoNativeEffects(t *testing.T) {
	bindings := map[instances.InstanceID]services.TrafficResetBinding{"disabled": {}}
	service := services.NewTrafficCountersResetService(bindings)
	delete(bindings, "disabled")
	for id, cause := range map[instances.InstanceID]error{"disabled": domain.ErrAPIDisabled, "missing": instances.ErrNotFound} {
		if _, err := service.ResetTrafficCounters(t.Context(), id, domain.UserTraffic, 1); !errors.Is(err, cause) {
			t.Fatalf("selection: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.ResetTrafficCounters(ctx, "missing", domain.UserTraffic, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("admission: %v", err)
	}
}
