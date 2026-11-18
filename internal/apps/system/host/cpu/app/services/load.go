// Package services coordinates fresh, measured host load reads.
package services

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
)

type Reader interface {
	Read(context.Context) (domain.Averages, error)
}

type Service struct{ Reader Reader }

func (service Service) Read(ctx context.Context) (domain.Observation, error) {
	started := time.Now()
	averages, err := service.Reader.Read(ctx)
	if err != nil {
		return domain.Observation{}, err
	}
	completed := time.Now()
	return domain.Observation{Averages: averages, ObservedAt: completed.UTC(), Duration: completed.Sub(started)}, nil
}
