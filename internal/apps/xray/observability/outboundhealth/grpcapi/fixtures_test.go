package grpcapi

import (
	"context"
	"errors"
	"math"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type reader struct {
	err   error
	calls int
}

func (r *reader) Read(ctx context.Context) ([]domain.Record, error) {
	r.calls++
	return []domain.Record{{Tag: "", DelayMS: math.MinInt64, HealthPing: &domain.HealthPing{All: math.MaxInt64}}}, errors.Join(r.err, ctx.Err())
}

type running struct{}

func (running) Status(context.Context, string) (instances.Status, error) {
	return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: math.MaxUint64}}, nil
}
