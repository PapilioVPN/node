package services_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
)

func TestActivityServicePreservesFreshCountersAndCauses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	calls := 0
	want := domain.Activity{ContextSwitches: 9007199254740993, Interrupts: 1<<64 - 1, SoftInterrupts: 1, CreatedTasks: 2, RunnableTasks: 0, IOBlockedTasks: 3}
	service := services.Service{ActivityReader: activityReaderFunc(func(got context.Context) (domain.Activity, error) {
		if got != ctx {
			t.Error("context replaced")
		}
		calls++
		return want, nil
	})}
	started := time.Now()
	got, err := service.ReadActivity(ctx)
	if err != nil || calls != 1 || got.Activity != want || got.ObservedAt.Before(started) || got.ObservedAt.After(time.Now()) || got.Duration < 0 || got.Duration > time.Since(started) {
		t.Fatalf("activity: %+v %v calls=%d", got, err, calls)
	}
	cause := errors.New("native read failed")
	for _, failure := range []error{cause, context.Canceled, context.DeadlineExceeded} {
		service.ActivityReader = activityReaderFunc(func(context.Context) (domain.Activity, error) { return want, failure })
		got, err := service.ReadActivity(ctx)
		if !errors.Is(err, failure) || got != (domain.ActivityObservation{}) {
			t.Fatalf("partial failure: %+v %v", got, err)
		}
	}
}
