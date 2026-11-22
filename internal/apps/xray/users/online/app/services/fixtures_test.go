package services

import (
	"context"
	"fmt"

	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
)

type reader struct {
	count func(context.Context, string) (domain.Summary, error)
	ips   func(context.Context, string) (domain.Visitors, error)
}

func (r reader) Count(ctx context.Context, email string) (domain.Summary, error) {
	return r.count(ctx, email)
}
func (r reader) IPs(ctx context.Context, email string) (domain.Visitors, error) {
	return r.ips(ctx, email)
}

type statusFunc func(context.Context, string) (instances.Status, error)

func (f statusFunc) Status(ctx context.Context, id string) (instances.Status, error) {
	return f(ctx, id)
}
func running(context.Context, string) (instances.Status, error) {
	return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: 7}}, nil
}

type collectionReader struct {
	calls int
	size  int
}

func (r *collectionReader) Names(context.Context) ([]domain.Record, error) {
	r.calls++
	rows := make([]domain.Record, r.size)
	for i := range rows {
		rows[i].Email = fmt.Sprintf("email-%04d", i)
	}
	return rows, nil
}
func (r *collectionReader) Records(ctx context.Context) ([]domain.Record, error) { return r.Names(ctx) }

type collectionFunc func(context.Context) ([]domain.Record, error)

func (f collectionFunc) Names(ctx context.Context) ([]domain.Record, error)   { return f(ctx) }
func (f collectionFunc) Records(ctx context.Context) ([]domain.Record, error) { return f(ctx) }
