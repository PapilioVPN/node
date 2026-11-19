package services_test

import (
	"context"
	"errors"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/domain"
)

type reader struct {
	calls int
	cause error
}

func (r *reader) Info(context.Context) (domain.Info, error) {
	r.calls++
	return domain.Info{PID: 42, GoVersion: "fixture"}, r.cause
}
func (r *reader) Resources(context.Context) (domain.Resources, error) {
	r.calls++
	return domain.Resources{PID: 42, StartTicks: 9007199254740993}, r.cause
}
func TestFreshTargetedReadsErrorCausesAndCancellation(t *testing.T) {
	r := &reader{}
	service := services.Service{Reader: r}
	ctx := context.Background()
	info, err := service.Info(ctx)
	if err != nil || info.Info.PID != 42 || info.ObservedAt.IsZero() || r.calls != 1 {
		t.Fatalf("%+v %v", info, err)
	}
	resources, err := service.Resources(ctx)
	if err != nil || resources.Resources.StartTicks != 9007199254740993 || r.calls != 2 {
		t.Fatalf("%+v %v", resources, err)
	}
	cause := errors.New("source unavailable")
	r.cause = cause
	if _, err := service.Resources(ctx); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	before := r.calls
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := service.Info(ctx); !errors.Is(err, context.Canceled) || r.calls != before {
		t.Fatal(err)
	}
}
