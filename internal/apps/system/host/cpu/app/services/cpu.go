// Package services coordinates fresh, measured host processor observations.
package services

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
)

type Reader interface {
	Read(context.Context) (domain.Averages, error)
}

type TimesReader interface {
	Read(context.Context, bool) (domain.Times, error)
}

type Service struct {
	Reader      Reader
	TimesReader TimesReader
}

func (service Service) Read(ctx context.Context) (domain.Observation, error) {
	started := time.Now()
	averages, err := service.Reader.Read(ctx)
	if err != nil {
		return domain.Observation{}, err
	}
	completed := time.Now()
	return domain.Observation{Averages: averages, ObservedAt: completed.UTC(), Duration: completed.Sub(started)}, nil
}

func (service Service) ReadTimes(ctx context.Context, perCPU bool) (domain.TimesObservation, error) {
	started := time.Now()
	times, err := service.TimesReader.Read(ctx, perCPU)
	if err != nil {
		return domain.TimesObservation{}, err
	}
	completed := time.Now()
	return domain.TimesObservation{Times: times, ObservedAt: completed.UTC(), Duration: completed.Sub(started)}, nil
}
