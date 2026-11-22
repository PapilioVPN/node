package grpcapi

import (
	"context"
	"fmt"

	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
)

type pageReader struct{ calls int }

func (r *pageReader) Count(context.Context, string) (domain.Summary, error) {
	panic("unexpected count")
}
func (r *pageReader) IPs(context.Context, string) (domain.Visitors, error) {
	panic("unexpected per-user read")
}
func (r *pageReader) Names(ctx context.Context) ([]domain.Record, error) {
	if _, ok := ctx.Deadline(); !ok {
		panic("missing deadline")
	}
	r.calls++
	rows := make([]domain.Record, 51)
	for i := range rows {
		rows[i] = domain.Record{Email: fmt.Sprintf("email-%02d", i), IP: &domain.IP{Address: "native", LastSeenUnixSeconds: -1}}
	}
	return rows, nil
}
func (r *pageReader) Records(ctx context.Context) ([]domain.Record, error) { return r.Names(ctx) }

type statusReader struct{}

func (statusReader) Status(context.Context, string) (instances.Status, error) {
	return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: 1}}, nil
}
