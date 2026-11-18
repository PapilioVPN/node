package services_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
)

func TestTimesUsesOneTargetedReadForAggregateAndGrowingCollections(t *testing.T) {
	for _, count := range []int{1, 64, 4096} {
		rows := make([]domain.Ticks, count)
		for i := range rows {
			rows[i] = domain.Ticks{CPU: fmt.Sprintf("cpu%d", i), User: 1<<64 - 1, Idle: uint64(i)}
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		calls := 0
		service := services.Service{TimesReader: timesReaderFunc(func(got context.Context, perCPU bool) (domain.Times, error) {
			if got != ctx || perCPU != (count > 1) {
				t.Fatal("lost caller context or selection")
			}
			calls++
			return domain.Times{PerCPU: perCPU, TicksPerSecond: 100, Counters: rows}, nil
		})}
		before := time.Now()
		result, err := service.ReadTimes(ctx, count > 1)
		if err != nil || calls != 1 || !reflect.DeepEqual(result.Times.Counters, rows) || result.ObservedAt.Before(before) || result.ObservedAt.After(time.Now()) || result.ObservedAt.Location() != time.UTC || result.Duration < 0 || result.Duration > time.Since(before) {
			t.Fatalf("times for %d CPUs: %+v %v; calls=%d", count, result, err, calls)
		}
		cancel()
	}
}

func TestTimesFailurePreservesCauseWithoutPartialCounters(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("private native cause")} {
		service := services.Service{TimesReader: timesReaderFunc(func(context.Context, bool) (domain.Times, error) {
			return domain.Times{Counters: []domain.Ticks{{CPU: "cpu9", User: 42}}}, cause
		})}
		got, err := service.ReadTimes(context.Background(), true)
		if !errors.Is(err, cause) || !reflect.DeepEqual(got, domain.TimesObservation{}) {
			t.Fatalf("lost cause or partial times: %+v %v", got, err)
		}
	}
}
