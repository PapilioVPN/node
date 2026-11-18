package grpcapi_test

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/system/host/identity/domain"
)

type readerFunc func(context.Context) (domain.Identity, error)

func (read readerFunc) Read(ctx context.Context) (domain.Identity, error) { return read(ctx) }
