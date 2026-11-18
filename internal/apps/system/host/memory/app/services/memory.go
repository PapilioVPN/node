// Package services coordinates fresh, measured host memory reads.
package services

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/memory/domain"
)

type Reader interface {
	Read(context.Context) (domain.Snapshot, error)
}

type SwapReader interface {
	Read(context.Context) (domain.SwapSnapshot, error)
}

// Service coordinates the focused RAM and swap observations of host memory.
type Service struct {
	Reader     Reader
	SwapReader SwapReader
}

func (service Service) Read(ctx context.Context) (domain.Observation, error) {
	started := time.Now()
	memory, err := service.Reader.Read(ctx)
	if err != nil {
		return domain.Observation{}, err
	}
	completed := time.Now()
	return domain.Observation{Memory: memory, ObservedAt: completed.UTC(), Duration: completed.Sub(started)}, nil
}

func (service Service) ReadSwap(ctx context.Context) (domain.SwapObservation, error) {
	started := time.Now()
	swap, err := service.SwapReader.Read(ctx)
	if err != nil {
		return domain.SwapObservation{}, err
	}
	completed := time.Now()
	return domain.SwapObservation{Swap: swap, ObservedAt: completed.UTC(), Duration: completed.Sub(started)}, nil
}
