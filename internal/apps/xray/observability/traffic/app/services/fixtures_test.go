package services

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type readerFunc func(context.Context, domain.Selection) (int64, error)

func (f readerFunc) Read(ctx context.Context, selection domain.Selection) (int64, error) {
	return f(ctx, selection)
}

type statusFunc func(context.Context, string) (instances.Status, error)

func (f statusFunc) Status(ctx context.Context, id string) (instances.Status, error) {
	return f(ctx, id)
}
func running(context.Context, string) (instances.Status, error) {
	return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: 7}}, nil
}

var exact = domain.Selection{Scope: domain.User, Name: " A>>>B ", Direction: domain.Uplink}
