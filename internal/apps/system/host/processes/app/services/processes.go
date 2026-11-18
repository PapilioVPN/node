// Package services coordinates focused process observations without native SDK types.
package services

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/processes/domain"
)

type Reader interface {
	Count(context.Context) (uint64, error)
	Read(context.Context, int32) (domain.Process, error)
	List(context.Context, domain.Selection) (domain.Page, error)
}

type Service struct {
	Reader         Reader
	TicksPerSecond uint64
}

func (service Service) Count(ctx context.Context) (domain.CountObservation, error) {
	started := time.Now()
	count, err := service.Reader.Count(ctx)
	if err != nil {
		return domain.CountObservation{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.CountObservation{}, err
	}
	completed := time.Now()
	return domain.CountObservation{Count: count, ObservedAt: completed.UTC(), Duration: completed.Sub(started)}, nil
}

func (service Service) Read(ctx context.Context, pid int32) (domain.Observation, error) {
	if pid <= 0 {
		return domain.Observation{}, domain.ErrInvalidArgument
	}
	started := time.Now()
	process, err := service.Reader.Read(ctx, pid)
	if err != nil {
		return domain.Observation{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.Observation{}, err
	}
	completed := time.Now()
	return domain.Observation{Process: process, TicksPerSecond: service.TicksPerSecond, ObservedAt: completed.UTC(), Duration: completed.Sub(started)}, nil
}

func (service Service) List(ctx context.Context, selection domain.Selection) (domain.ListObservation, error) {
	if selection.AfterPID < 0 || selection.Limit > 256 {
		return domain.ListObservation{}, domain.ErrInvalidArgument
	}
	if selection.Limit == 0 {
		selection.Limit = 128
	}
	started := time.Now()
	page, err := service.Reader.List(ctx, selection)
	if err != nil {
		return domain.ListObservation{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.ListObservation{}, err
	}
	completed := time.Now()
	return domain.ListObservation{Page: page, TicksPerSecond: service.TicksPerSecond, ObservedAt: completed.UTC(), Duration: completed.Sub(started)}, nil
}
