// Package services coordinates measured host identity reads.
package services

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/identity/domain"
)

type Reader interface {
	Read(context.Context) (domain.Identity, error)
}

type Service struct {
	Reader Reader
}

func (service Service) Read(ctx context.Context) (domain.Observation, error) {
	started := time.Now()
	identity, err := service.Reader.Read(ctx)
	if err != nil {
		return domain.Observation{}, err
	}
	completed := time.Now()
	return domain.Observation{
		Identity: identity, ObservedAt: completed.UTC(), Duration: completed.Sub(started),
	}, nil
}
