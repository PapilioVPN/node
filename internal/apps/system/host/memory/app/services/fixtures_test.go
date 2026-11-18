package services_test

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/system/host/memory/domain"
)

type readerFunc func(context.Context) (domain.Snapshot, error)

func (read readerFunc) Read(ctx context.Context) (domain.Snapshot, error) { return read(ctx) }

type swapReaderFunc func(context.Context) (domain.SwapSnapshot, error)

func (read swapReaderFunc) Read(ctx context.Context) (domain.SwapSnapshot, error) { return read(ctx) }
