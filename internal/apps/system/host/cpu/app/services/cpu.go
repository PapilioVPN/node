// Package services coordinates fresh, measured host processor observations.
package services

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/app/policies"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
)

type Reader interface {
	Read(context.Context) (domain.Averages, error)
}

type TimesReader interface {
	Read(context.Context, bool) (domain.Times, error)
}

type InfoReader interface {
	Read(context.Context) ([]domain.Processor, error)
}

type FrequencyReader interface {
	Read(context.Context) (domain.Frequencies, error)
}

type ActivityReader interface {
	Read(context.Context) (domain.Activity, error)
}

type Service struct {
	ActivityReader  ActivityReader
	FrequencyReader FrequencyReader
	InfoReader      InfoReader
	Reader          Reader
	TimesReader     TimesReader
}

func (service Service) ReadFrequency(ctx context.Context) (domain.FrequencyObservation, error) {
	started := time.Now()
	frequencies, err := service.FrequencyReader.Read(ctx)
	if err != nil {
		return domain.FrequencyObservation{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.FrequencyObservation{}, err
	}
	completed := time.Now()
	return domain.FrequencyObservation{Frequencies: frequencies, ObservedAt: completed.UTC(), Duration: completed.Sub(started)}, nil
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

func (service Service) ReadInfo(ctx context.Context) (domain.InfoObservation, error) {
	started := time.Now()
	rows, err := service.InfoReader.Read(ctx)
	if err != nil {
		return domain.InfoObservation{}, err
	}
	info := policies.Info(rows)
	if err := ctx.Err(); err != nil {
		return domain.InfoObservation{}, err
	}
	completed := time.Now()
	return domain.InfoObservation{Info: info, ObservedAt: completed.UTC(), Duration: completed.Sub(started)}, nil
}

func (service Service) ReadActivity(ctx context.Context) (domain.ActivityObservation, error) {
	started := time.Now()
	activity, err := service.ActivityReader.Read(ctx)
	if err != nil {
		return domain.ActivityObservation{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.ActivityObservation{}, err
	}
	completed := time.Now()
	return domain.ActivityObservation{Activity: activity, ObservedAt: completed.UTC(), Duration: completed.Sub(started)}, nil
}
