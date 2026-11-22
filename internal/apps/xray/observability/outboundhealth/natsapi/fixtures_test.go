package natsapi

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type reader struct{ calls int }

func (r *reader) Read(ctx context.Context) ([]domain.Record, error) { r.calls++; return nil, ctx.Err() }

type running struct{}

func (running) Status(context.Context, string) (instances.Status, error) {
	return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: 1}}, nil
}
