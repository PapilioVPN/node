package natsapi

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type readerFixture struct {
	calls     int
	err       error
	remaining time.Duration
}

func (r *readerFixture) Read(ctx context.Context, _ domain.Selection) (int64, error) {
	r.calls++
	deadline, _ := ctx.Deadline()
	r.remaining = time.Until(deadline)
	return -9007199254740993, r.err
}

type statusFixture struct{}

func (statusFixture) Status(context.Context, string) (instances.Status, error) {
	return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: 1}}, nil
}

func (r *readerFixture) Reset(ctx context.Context, selection domain.Selection) (int64, error) {
	return r.Read(ctx, selection)
}
