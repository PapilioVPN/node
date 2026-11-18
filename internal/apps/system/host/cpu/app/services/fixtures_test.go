package services_test

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
)

type readerFunc func(context.Context) (domain.Averages, error)

func (read readerFunc) Read(ctx context.Context) (domain.Averages, error) { return read(ctx) }

type timesReaderFunc func(context.Context, bool) (domain.Times, error)

func (read timesReaderFunc) Read(ctx context.Context, perCPU bool) (domain.Times, error) {
	return read(ctx, perCPU)
}

type infoReaderFunc func(context.Context) ([]domain.Processor, error)

func (read infoReaderFunc) Read(ctx context.Context) ([]domain.Processor, error) { return read(ctx) }

type frequencyReaderFunc func(context.Context) (domain.Frequencies, error)

func (read frequencyReaderFunc) Read(ctx context.Context) (domain.Frequencies, error) {
	return read(ctx)
}

type activityReaderFunc func(context.Context) (domain.Activity, error)

func (read activityReaderFunc) Read(ctx context.Context) (domain.Activity, error) { return read(ctx) }
