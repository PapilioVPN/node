// Package services coordinates fresh, measured uptime observations.
package services

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/uptime/domain"
)

type Reader interface {
	Read(context.Context) (time.Duration, error)
}

type Service struct {
	Reader Reader
}

func (service Service) Read(ctx context.Context) (domain.Observation, error) {
	started := time.Now()
	uptime, err := service.Reader.Read(ctx)
	if err != nil {
		return domain.Observation{}, err
	}
	completed := time.Now()
	return domain.Observation{
		Uptime: uptime, ObservedAt: completed.UTC(), Duration: completed.Sub(started),
	}, nil
}
