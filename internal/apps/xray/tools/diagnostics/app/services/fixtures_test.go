package services

import (
	"context"
	"time"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/domain"
)

type statusFunc func(context.Context, string) (instances.Status, error)

func (f statusFunc) Status(ctx context.Context, id string) (instances.Status, error) {
	return f(ctx, id)
}
func running(context.Context, string) (instances.Status, error) {
	return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: 7}, ObservedAt: time.Now().UTC()}, nil
}

type metadataFunc func(context.Context, string) (configs.DesiredMetadata, error)

func (f metadataFunc) Metadata(ctx context.Context, id string) (configs.DesiredMetadata, error) {
	return f(ctx, id)
}

type binaryFunc func(context.Context) (domain.Binary, error)

func (f binaryFunc) Read(ctx context.Context) (domain.Binary, error) { return f(ctx) }

type nativeFunc func(context.Context) (domain.Native, error)

func (f nativeFunc) Read(ctx context.Context) (domain.Native, error) { return f(ctx) }
