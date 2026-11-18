package services_test

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/system/host/memory/domain"
)

type readerFunc func(context.Context) (domain.Snapshot, error)

func (read readerFunc) Read(ctx context.Context) (domain.Snapshot, error) { return read(ctx) }
