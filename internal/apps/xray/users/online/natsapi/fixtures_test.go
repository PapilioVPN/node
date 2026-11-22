package natsapi

import (
	"context"
	"fmt"

	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
)

type reader struct{ calls int }

func (r *reader) Count(ctx context.Context, email string) (domain.Summary, error) {
	r.calls++
	if _, ok := ctx.Deadline(); !ok {
		panic("missing deadline")
	}
	return domain.Summary{}, nil
}
func (r *reader) IPs(context.Context, string) (domain.Visitors, error) {
	r.calls++
	return domain.Visitors{Tracked: true}, nil
}

type statusReader struct{}

func (statusReader) Status(context.Context, string) (instances.Status, error) {
	return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: 9}}, nil
}

func (r *reader) Names(ctx context.Context) ([]domain.Record, error) {
	if _, ok := ctx.Deadline(); !ok {
		panic("missing deadline")
	}
	r.calls++
	rows := make([]domain.Record, 51)
	for i := range rows {
		rows[i].Email = fmt.Sprintf("email-%02d", i)
	}
	return rows, nil
}
func (r *reader) Records(ctx context.Context) ([]domain.Record, error) { return r.Names(ctx) }
