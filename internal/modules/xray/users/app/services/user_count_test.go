package services_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/users/app/services"
	"github.com/PapilioVPN/node/internal/modules/xray/users/domain"
)

type userCountReader func(context.Context, string) (domain.InboundUserCount, error)

func (f userCountReader) ReadInboundUserCount(ctx context.Context, tag string) (domain.InboundUserCount, error) {
	return f(ctx, tag)
}

func TestUserCountSelectsOneInstanceWithoutListingAndPreservesNativePrecision(t *testing.T) {
	ctx := t.Context()
	want := domain.InboundUserCount{Count: math.MaxInt64, ObservedAt: time.UnixMilli(1700000000123)}
	calls := 0
	bindings := map[instances.InstanceID]services.InboundUserCountReader{" edge ": userCountReader(func(received context.Context, tag string) (domain.InboundUserCount, error) {
		calls++
		if received != ctx || tag != " exact>>>tag " {
			t.Fatal("count selection/context changed")
		}
		return want, nil
	}), "disabled": nil}
	service := services.NewUserCountService(bindings)
	delete(bindings, " edge ")
	result, err := service.GetInboundUserCount(ctx, " edge ", " exact>>>tag ")
	if err != nil || result.InstanceID != " edge " || result.InboundTag != " exact>>>tag " || result.Users != want || calls != 1 {
		t.Fatalf("native count changed: %+v %v", result, err)
	}
	for id, cause := range map[instances.InstanceID]error{"edge": instances.ErrNotFound, "disabled": domain.ErrAPIDisabled} {
		if _, err := service.GetInboundUserCount(ctx, id, "in"); !errors.Is(err, cause) || calls != 1 {
			t.Fatalf("count crossed instance selection: %v", err)
		}
	}
}

func TestUserCountReadFailuresAndLateCancellationAreNeverSuccessfulZero(t *testing.T) {
	for _, cause := range []error{domain.ErrUserReadUnsupported, domain.ErrAPIResourceLimit, errors.New("private native diagnostics"), nil} {
		ctx, cancel := context.WithCancel(t.Context())
		calls := 0
		service := services.NewUserCountService(map[instances.InstanceID]services.InboundUserCountReader{"edge": userCountReader(func(context.Context, string) (domain.InboundUserCount, error) {
			calls++
			if cause == nil {
				cancel()
			}
			return domain.InboundUserCount{Count: 100, ObservedAt: time.Now()}, cause
		})})
		result, err := service.GetInboundUserCount(ctx, "edge", "in")
		cancel()
		if result != (domain.InstanceInboundUserCount{}) || calls != 1 || (cause != nil && !errors.Is(err, cause)) || (cause == nil && !errors.Is(err, context.Canceled)) {
			t.Fatalf("failed observation became successful facts: %+v %v", result, err)
		}
		if _, err := service.GetInboundUserCount(ctx, "edge", "in"); !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatal("pre-canceled count reached native reader")
		}
	}
}
