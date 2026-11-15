package services_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/app/services"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

func TestTrafficPairResetKeepsMissingDistinctFromAcknowledgedZero(t *testing.T) {
	for _, presence := range []int{0, 1, 2, 3} {
		calls := 0
		var deadline time.Time
		selection := domain.TrafficPairSelection{Scope: domain.OutboundTraffic, Name: " exact>>>tag "}
		acknowledged := time.UnixMilli(1700000000123)
		bindings := map[instances.InstanceID]services.TrafficPairResetBinding{" edge ": {Timeout: time.Second, Resetter: trafficResetter(func(ctx context.Context, query domain.TrafficSelection) (domain.TrafficCounterReset, error) {
			if query.Scope != selection.Scope || query.Name != selection.Name || query.Direction != [2]domain.TrafficDirection{domain.Uplink, domain.Downlink}[calls] {
				t.Fatal("reset changed exact selection")
			}
			actual, ok := ctx.Deadline()
			if !ok {
				t.Fatal("no operation deadline")
			}
			if calls == 0 {
				deadline = actual
			} else if !deadline.Equal(actual) {
				t.Fatal("reset budget extended")
			}
			calls++
			if presence&(1<<(calls-1)) == 0 {
				return domain.TrafficCounterReset{}, domain.ErrTrafficCounterNotFound
			}
			return domain.TrafficCounterReset{PreviousBytes: [2]int64{0, math.MaxInt64}[calls-1], AcknowledgedAt: acknowledged}, nil
		})}}
		service := services.NewTrafficPairResetService(bindings)
		delete(bindings, " edge ")
		result, err := service.ResetTrafficPair(t.Context(), " edge ", selection)
		if err != nil || calls != 2 || result.InstanceID != " edge " || result.Selection != selection {
			t.Fatalf("reset: %+v %v calls=%d", result, err, calls)
		}
		for i, item := range result.Items {
			if item.Name != selection.Name || item.Direction != [2]domain.TrafficDirection{domain.Uplink, domain.Downlink}[i] {
				t.Fatal("progress identity changed")
			}
			if presence&(1<<i) == 0 {
				if item.State != domain.TrafficResetMissing || item.Reset != nil {
					t.Fatalf("absence became usage: %+v", item)
				}
				continue
			}
			if item.State != domain.TrafficResetAcknowledged || item.Reset == nil || item.Reset.PreviousBytes != [2]int64{0, math.MaxInt64}[i] || item.Reset.AcknowledgedAt != acknowledged {
				t.Fatalf("ack lost: %+v", item)
			}
		}
	}
}

func TestTrafficPairResetStopsOnFailureWithoutDiscardingAcknowledgedEffects(t *testing.T) {
	for _, cause := range []error{domain.ErrAPIBusy, domain.ErrTrafficUnsupported, errors.Join(domain.ErrTrafficResetUnknown, context.DeadlineExceeded), errors.Join(domain.ErrTrafficResetUnknown, domain.ErrTrafficCounterNotFound)} {
		for _, failedCall := range []int{1, 2} {
			calls := 0
			service := services.NewTrafficPairResetService(map[instances.InstanceID]services.TrafficPairResetBinding{"edge": {Timeout: time.Second, Resetter: trafficResetter(func(context.Context, domain.TrafficSelection) (domain.TrafficCounterReset, error) {
				calls++
				if calls == failedCall {
					return domain.TrafficCounterReset{PreviousBytes: 99}, cause
				}
				return domain.TrafficCounterReset{PreviousBytes: math.MinInt64, AcknowledgedAt: time.Now()}, nil
			})}})
			result, err := service.ResetTrafficPair(t.Context(), "edge", domain.TrafficPairSelection{Scope: domain.UserTraffic, Name: "user"})
			if !errors.Is(err, cause) || calls != failedCall {
				t.Fatalf("failure retried/ignored: %+v %v calls=%d", result, err, calls)
			}
			for i, item := range result.Items {
				want := domain.TrafficResetNotAttempted
				if i+1 < failedCall {
					want = domain.TrafficResetAcknowledged
				}
				if i+1 == failedCall {
					want = domain.TrafficResetNotApplied
					if errors.Is(cause, domain.ErrTrafficResetUnknown) {
						want = domain.TrafficResetUnknown
					}
				}
				if item.State != want || (item.Reset != nil) != (want == domain.TrafficResetAcknowledged) {
					t.Fatalf("effects lost: %+v", result)
				}
			}
		}
	}
}

func TestTrafficPairResetKeepsLateAcknowledgementsAndUnattemptedRemainder(t *testing.T) {
	for _, cancelAfter := range []int{1, 2} {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		calls := 0
		service := services.NewTrafficPairResetService(map[instances.InstanceID]services.TrafficPairResetBinding{"edge": {Timeout: time.Second, Resetter: trafficResetter(func(context.Context, domain.TrafficSelection) (domain.TrafficCounterReset, error) {
			calls++
			if calls == cancelAfter {
				cancel()
			}
			return domain.TrafficCounterReset{PreviousBytes: 7, AcknowledgedAt: time.Now()}, nil
		})}})
		result, err := service.ResetTrafficPair(ctx, "edge", domain.TrafficPairSelection{Scope: domain.UserTraffic, Name: "user"})
		if cancelAfter == 1 && !errors.Is(err, context.Canceled) || cancelAfter == 2 && err != nil || calls != cancelAfter || result.Items[0].State != domain.TrafficResetAcknowledged || result.Items[0].Reset == nil || cancelAfter == 1 && result.Items[1].State != domain.TrafficResetNotAttempted || cancelAfter == 2 && result.Items[1].Reset == nil {
			t.Fatalf("late cancellation erased effects: %+v %v", result, err)
		}
	}
	service := services.NewTrafficPairResetService(map[instances.InstanceID]services.TrafficPairResetBinding{"disabled": {}})
	for id, cause := range map[instances.InstanceID]error{"missing": instances.ErrNotFound, "disabled": domain.ErrAPIDisabled} {
		if report, err := service.ResetTrafficPair(t.Context(), id, domain.TrafficPairSelection{Name: "user"}); !errors.Is(err, cause) || report.Items[0].State != domain.TrafficResetNotAttempted || report.Items[1].State != domain.TrafficResetNotAttempted {
			t.Fatalf("preflight: %+v %v", report, err)
		}
	}
}
