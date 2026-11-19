// Package services coordinates focused physical sensor observations.
package services

import (
	"context"
	"github.com/PapilioVPN/node/internal/apps/system/host/sensors/domain"
	"time"
)

type Reader interface {
	Temperatures(context.Context) ([]domain.Temperature, error)
	Fans(context.Context) ([]domain.Fan, error)
	PowerSupplies(context.Context) ([]domain.PowerSupply, error)
}
type Service struct{ Reader Reader }

func (s Service) Temperatures(ctx context.Context) (domain.Temperatures, error) {
	if err := ctx.Err(); err != nil {
		return domain.Temperatures{}, err
	}
	start := time.Now()
	rows, err := s.Reader.Temperatures(ctx)
	if err != nil {
		return domain.Temperatures{}, err
	}
	if err = ctx.Err(); err != nil {
		return domain.Temperatures{}, err
	}
	end := time.Now()
	return domain.Temperatures{Readings: rows, ObservedAt: end.UTC(), Duration: end.Sub(start)}, nil
}
func (s Service) Fans(ctx context.Context) (domain.Fans, error) {
	if err := ctx.Err(); err != nil {
		return domain.Fans{}, err
	}
	start := time.Now()
	rows, err := s.Reader.Fans(ctx)
	if err != nil {
		return domain.Fans{}, err
	}
	if err = ctx.Err(); err != nil {
		return domain.Fans{}, err
	}
	end := time.Now()
	return domain.Fans{Readings: rows, ObservedAt: end.UTC(), Duration: end.Sub(start)}, nil
}
func (s Service) PowerSupplies(ctx context.Context) (domain.PowerSupplies, error) {
	if err := ctx.Err(); err != nil {
		return domain.PowerSupplies{}, err
	}
	start := time.Now()
	rows, err := s.Reader.PowerSupplies(ctx)
	if err != nil {
		return domain.PowerSupplies{}, err
	}
	if err = ctx.Err(); err != nil {
		return domain.PowerSupplies{}, err
	}
	end := time.Now()
	return domain.PowerSupplies{Readings: rows, ObservedAt: end.UTC(), Duration: end.Sub(start)}, nil
}
