// Package services coordinates fresh, measured network interface observations.
package services

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/domain"
)

type Reader interface {
	List(context.Context) (domain.Snapshot, error)
	Counters(context.Context) ([]domain.InterfaceCounters, error)
}

type Service struct{ Reader Reader }

func (service Service) List(ctx context.Context) (domain.Observation, error) {
	started := time.Now()
	snapshot, err := service.Reader.List(ctx)
	if err != nil {
		return domain.Observation{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.Observation{}, err
	}
	completed := time.Now()
	return domain.Observation{Snapshot: snapshot, ObservedAt: completed.UTC(), Duration: completed.Sub(started)}, nil
}

func (service Service) ReadCounters(ctx context.Context) (domain.CounterObservation, error) {
	started := time.Now()
	rows, err := service.Reader.Counters(ctx)
	if err != nil {
		return domain.CounterObservation{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.CounterObservation{}, err
	}
	completed := time.Now()
	return domain.CounterObservation{Interfaces: rows, ObservedAt: completed.UTC(), Duration: completed.Sub(started)}, nil
}
