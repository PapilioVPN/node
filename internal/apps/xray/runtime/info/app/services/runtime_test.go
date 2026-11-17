package services_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/info/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/info/domain"
)

type runtimeReader func(context.Context) (domain.RuntimeInfo, error)

func (f runtimeReader) ReadRuntimeInfo(ctx context.Context) (domain.RuntimeInfo, error) {
	return f(ctx)
}

func TestRuntimeInfoSelectsOneExactBindingAndPreservesUnsignedFacts(t *testing.T) {
	ctx := t.Context()
	info := domain.RuntimeInfo{AllocatedBytes: math.MaxUint64, UptimeSeconds: math.MaxUint32, ObservedAt: time.Now()}
	calls := 0
	readers := map[instances.InstanceID]services.RuntimeInfoReader{
		"edge": runtimeReader(func(received context.Context) (domain.RuntimeInfo, error) {
			calls++
			if received != ctx {
				t.Fatal("context changed")
			}
			return info, nil
		}),
		"other": runtimeReader(func(context.Context) (domain.RuntimeInfo, error) {
			t.Fatal("unselected API called")
			return domain.RuntimeInfo{}, nil
		}),
		"disabled": nil,
	}
	service := services.NewRuntimeInfoService(readers)
	delete(readers, "edge")
	result, err := service.GetRuntimeInfo(ctx, "edge")
	if err != nil || result.InstanceID != "edge" || result.Runtime != info || calls != 1 {
		t.Fatalf("selection or facts changed: %+v, %v; calls=%d", result, err, calls)
	}
	for id, cause := range map[instances.InstanceID]error{" edge ": instances.ErrNotFound, "missing": instances.ErrNotFound, "disabled": domain.ErrAPIDisabled} {
		if _, err := service.GetRuntimeInfo(ctx, id); !errors.Is(err, cause) {
			t.Fatalf("selection %q: %v", id, err)
		}
	}
}

func TestRuntimeInfoPreservesErrorsAndCancellationWithoutPartialFacts(t *testing.T) {
	cause := errors.New("native failure")
	for _, cancelDuringRead := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		service := services.NewRuntimeInfoService(map[instances.InstanceID]services.RuntimeInfoReader{"edge": runtimeReader(func(context.Context) (domain.RuntimeInfo, error) {
			if cancelDuringRead {
				cancel()
			}
			return domain.RuntimeInfo{Goroutines: 99}, cause
		})})
		result, err := service.GetRuntimeInfo(ctx, "edge")
		cancel()
		if result != (domain.InstanceRuntimeInfo{}) || !errors.Is(err, cause) || cancelDuringRead && !errors.Is(err, context.Canceled) {
			t.Fatalf("partial read or lost cause: %+v, %v", result, err)
		}
		if _, err := service.GetRuntimeInfo(ctx, "missing"); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled admission: %v", err)
		}
	}
}
