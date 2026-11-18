package services_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/domain"
)

func TestInterfaceServiceUsesFocusedFreshReads(t *testing.T) {
	for _, size := range []int{0, 1, 64, 4096} {
		fixture := &readerFixture{snapshot: domain.Snapshot{Interfaces: make([]domain.Interface, size)}, counters: make([]domain.InterfaceCounters, size)}
		service := services.Service{Reader: fixture}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		started := time.Now()
		got, err := service.List(ctx)
		if err != nil || fixture.calls != 1 || fixture.counterCalls != 0 || fixture.context != ctx || !reflect.DeepEqual(got.Snapshot, fixture.snapshot) || got.ObservedAt.Before(started) || got.ObservedAt.After(time.Now()) || got.Duration < 0 || got.Duration > time.Since(started) {
			t.Fatalf("inventory: %+v %v", got, err)
		}
		started = time.Now()
		counters, err := service.ReadCounters(ctx)
		cancel()
		if err != nil || fixture.calls != 1 || fixture.counterCalls != 1 || !reflect.DeepEqual(counters.Interfaces, fixture.counters) || counters.ObservedAt.Before(started) || counters.ObservedAt.After(time.Now()) || counters.Duration < 0 {
			t.Fatalf("counters: %+v %v", counters, err)
		}
	}
}

func TestInterfaceServiceDoesNotPublishPartialFailures(t *testing.T) {
	for _, cause := range []error{errors.New("native failure"), context.Canceled, context.DeadlineExceeded} {
		service := services.Service{Reader: &readerFixture{snapshot: domain.Snapshot{Interfaces: []domain.Interface{{Index: 1}}}, counters: []domain.InterfaceCounters{{Index: 1}}, cause: cause}}
		got, err := service.List(context.Background())
		if !errors.Is(err, cause) || got.Snapshot.Interfaces != nil || !got.ObservedAt.IsZero() {
			t.Fatalf("partial inventory: %+v %v", got, err)
		}
		rows, err := service.ReadCounters(context.Background())
		if !errors.Is(err, cause) || rows.Interfaces != nil || !rows.ObservedAt.IsZero() {
			t.Fatalf("partial counters: %+v %v", rows, err)
		}
	}
}
