package services_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/core/traffic/app/services"
	"github.com/PapilioVPN/node/internal/modules/core/traffic/domain"
)

func concurrentScopeService(size, concurrency int, reset trafficResetter) *services.TrafficCountersResetService {
	return services.NewTrafficCountersResetService(map[instances.InstanceID]services.TrafficResetBinding{"edge": {
		Timeout: 3 * time.Second, MaxConcurrent: concurrency, Resetter: reset,
		Reader: trafficListReader(func(context.Context, domain.TrafficScope) (domain.TrafficCounters, error) {
			items := make([]domain.TrafficCounterEntry, size)
			for i := range items {
				items[i] = domain.TrafficCounterEntry{Name: fmt.Sprintf("counter-%04d", i), Direction: domain.Uplink}
			}
			return domain.TrafficCounters{Items: items, ObservedAt: time.UnixMilli(1700000000123)}, nil
		}),
	}})
}

func TestScopeResetAdmitsTheConfiguredConcurrencyAndJoinsAllAcknowledgements(t *testing.T) {
	const size, limit = 7, 3
	started := make(chan string, size)
	release := make(chan struct{})
	var active, peak, calls atomic.Int32
	service := concurrentScopeService(size, limit, func(ctx context.Context, s domain.TrafficSelection) (domain.TrafficCounterReset, error) {
		calls.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for previous := peak.Load(); n > previous && !peak.CompareAndSwap(previous, n); previous = peak.Load() {
		}
		started <- s.Name
		select {
		case <-release:
		case <-ctx.Done():
			return domain.TrafficCounterReset{}, ctx.Err()
		}
		return domain.TrafficCounterReset{PreviousBytes: math.MinInt64, AcknowledgedAt: time.UnixMilli(1700000000124)}, nil
	})
	type outcome struct {
		report domain.InstanceTrafficCountersReset
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		r, e := service.ResetTrafficCounters(t.Context(), "edge", domain.UserTraffic, size)
		done <- outcome{r, e}
	}()
	for i := 0; i < limit; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(release)
			<-done
			t.Fatalf("only %d resets admitted before release; requested %d", i, limit)
		}
	}
	if active.Load() != limit || calls.Load() != limit {
		close(release)
		<-done
		t.Fatal("concurrency was unbounded or serialized")
	}
	close(release)
	result := <-done
	if result.err != nil || calls.Load() != size || peak.Load() != limit || active.Load() != 0 || len(result.report.Reset.Items) != size {
		t.Fatalf("bound/join failed: calls=%d peak=%d active=%d err=%v", calls.Load(), peak.Load(), active.Load(), result.err)
	}
	for i, item := range result.report.Reset.Items {
		if item.Name != fmt.Sprintf("counter-%04d", i) || item.State != domain.TrafficResetAcknowledged || item.Reset == nil || item.Reset.PreviousBytes != math.MinInt64 || item.Reset.AcknowledgedAt.UnixMilli() != 1700000000124 {
			t.Fatalf("completion order lost identity/ack: %+v", item)
		}
	}
}

func TestConcurrentScopeResetStopsAdmissionOnFailureAndRetainsEveryCause(t *testing.T) {
	const size, limit = 1000, 3
	var calls atomic.Int32
	cause := errors.Join(domain.ErrTrafficResetUnknown, domain.ErrAPIResourceLimit)
	service := concurrentScopeService(size, limit, func(context.Context, domain.TrafficSelection) (domain.TrafficCounterReset, error) {
		calls.Add(1)
		return domain.TrafficCounterReset{}, cause
	})
	report, err := service.ResetTrafficCounters(t.Context(), "edge", domain.UserTraffic, size)
	if !errors.Is(err, domain.ErrTrafficResetUnknown) || !errors.Is(err, domain.ErrAPIResourceLimit) || calls.Load() == 0 || calls.Load() > limit {
		t.Fatalf("failed operation kept dispatching: calls=%d err=%v", calls.Load(), err)
	}
	var attempted int
	for _, item := range report.Reset.Items {
		if item.State == domain.TrafficResetUnknown {
			attempted++
		} else if item.State != domain.TrafficResetNotAttempted {
			t.Fatalf("invented state: %+v", item)
		}
		if item.Reset != nil {
			t.Fatal("failed reset acquired acknowledgement")
		}
	}
	if attempted != int(calls.Load()) {
		t.Fatal("lost actual attempt identities")
	}
}

func TestConcurrentScopeResetJoinsPartialEffectsWithoutCancelingPeerAcknowledgements(t *testing.T) {
	const size = 3
	started := make(chan string, size)
	releaseFailure, releaseAcknowledgements := make(chan struct{}), make(chan struct{})
	service := concurrentScopeService(size, size, func(ctx context.Context, s domain.TrafficSelection) (domain.TrafficCounterReset, error) {
		started <- s.Name
		if s.Name == "counter-0001" {
			<-releaseFailure
			return domain.TrafficCounterReset{}, errors.Join(domain.ErrTrafficResetUnknown, errors.New("lost acknowledgement"))
		}
		<-releaseAcknowledgements
		if err := ctx.Err(); err != nil {
			return domain.TrafficCounterReset{}, err
		}
		return domain.TrafficCounterReset{PreviousBytes: 0, AcknowledgedAt: time.UnixMilli(1700000000124)}, nil
	})
	type outcome struct {
		report domain.InstanceTrafficCountersReset
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		r, e := service.ResetTrafficCounters(t.Context(), "edge", domain.UserTraffic, size)
		done <- outcome{r, e}
	}()
	for i := 0; i < size; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(releaseFailure)
			close(releaseAcknowledgements)
			<-done
			t.Fatal("not all initial resets were admitted")
		}
	}
	close(releaseFailure)
	close(releaseAcknowledgements)
	result := <-done
	if !errors.Is(result.err, domain.ErrTrafficResetUnknown) {
		t.Fatalf("failure cause lost: %v", result.err)
	}
	for _, item := range result.report.Reset.Items {
		if item.Name == "counter-0001" {
			if item.State != domain.TrafficResetUnknown || item.Reset != nil {
				t.Fatal("unknown effect lost identity")
			}
		} else if item.State != domain.TrafficResetAcknowledged || item.Reset == nil || item.Reset.PreviousBytes != 0 {
			t.Fatalf("peer acknowledgement was canceled/lost: %+v", item)
		}
	}
}

func TestConcurrentScopeResetCancellationStopsAdmissionAndJoinsUnknownEffects(t *testing.T) {
	const size, limit = 8, 3
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{}, limit)
	var active, calls atomic.Int32
	service := concurrentScopeService(size, limit, func(ctx context.Context, _ domain.TrafficSelection) (domain.TrafficCounterReset, error) {
		active.Add(1)
		defer active.Add(-1)
		calls.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		return domain.TrafficCounterReset{}, errors.Join(domain.ErrTrafficResetUnknown, ctx.Err())
	})
	type outcome struct {
		report domain.InstanceTrafficCountersReset
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		r, e := service.ResetTrafficCounters(ctx, "edge", domain.UserTraffic, size)
		done <- outcome{r, e}
	}()
	for i := 0; i < limit; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			cancel()
			<-done
			t.Fatal("resets were serialized")
		}
	}
	cancel()
	result := <-done
	if !errors.Is(result.err, context.Canceled) || !errors.Is(result.err, domain.ErrTrafficResetUnknown) || active.Load() != 0 || calls.Load() != limit {
		t.Fatalf("cancellation failed to bound/join: calls=%d active=%d err=%v", calls.Load(), active.Load(), result.err)
	}
	var unknown int
	for _, item := range result.report.Reset.Items {
		if item.State == domain.TrafficResetUnknown {
			unknown++
		} else if item.State != domain.TrafficResetNotAttempted {
			t.Fatalf("uninvoked work was changed: %+v", item)
		}
	}
	if unknown != limit {
		t.Fatal("lost canceled in-flight outcomes")
	}
}

func TestConcurrentScopeResetRetainsAcknowledgementsAfterCallerCancellation(t *testing.T) {
	const size, limit = 6, 3
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{}, limit)
	service := concurrentScopeService(size, limit, func(ctx context.Context, _ domain.TrafficSelection) (domain.TrafficCounterReset, error) {
		started <- struct{}{}
		<-ctx.Done()
		return domain.TrafficCounterReset{PreviousBytes: math.MaxInt64, AcknowledgedAt: time.UnixMilli(1700000000124)}, nil
	})
	type outcome struct {
		report domain.InstanceTrafficCountersReset
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		r, e := service.ResetTrafficCounters(ctx, "edge", domain.UserTraffic, size)
		done <- outcome{r, e}
	}()
	for i := 0; i < limit; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			cancel()
			<-done
			t.Fatal("resets were serialized")
		}
	}
	cancel()
	result := <-done
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("unattempted remainder lost cancellation: %v", result.err)
	}
	for i, item := range result.report.Reset.Items {
		if i < limit {
			if item.State != domain.TrafficResetAcknowledged || item.Reset == nil || item.Reset.PreviousBytes != math.MaxInt64 {
				t.Fatal("confirmed late effect was discarded")
			}
		} else if item.State != domain.TrafficResetNotAttempted || item.Reset != nil {
			t.Fatal("canceled admission fabricated an effect")
		}
	}
}
