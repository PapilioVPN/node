package services_test

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
)

type readerFunc func(context.Context) (domain.Averages, error)

func (read readerFunc) Read(ctx context.Context) (domain.Averages, error) { return read(ctx) }
