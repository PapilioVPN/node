package services

import (
	"context"

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
