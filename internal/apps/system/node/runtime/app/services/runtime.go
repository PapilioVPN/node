// Package services coordinates focused observations of Node's own process.
package services

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/domain"
)

type Reader interface {
	Info(context.Context) (domain.Info, error)
	Resources(context.Context) (domain.Resources, error)
}
type Service struct {
	Reader Reader
	Probes DiagnosticsReader
}

func (service Service) Info(ctx context.Context) (domain.InfoObservation, error) {
	if err := ctx.Err(); err != nil {
		return domain.InfoObservation{}, err
	}
	start := time.Now()
	info, err := service.Reader.Info(ctx)
	if err != nil {
		return domain.InfoObservation{}, err
	}
	if err = ctx.Err(); err != nil {
		return domain.InfoObservation{}, err
	}
	end := time.Now()
	return domain.InfoObservation{Info: info, ObservedAt: end.UTC(), Duration: end.Sub(start)}, nil
}
func (service Service) Resources(ctx context.Context) (domain.ResourceObservation, error) {
	if err := ctx.Err(); err != nil {
		return domain.ResourceObservation{}, err
	}
	start := time.Now()
	resources, err := service.Reader.Resources(ctx)
	if err != nil {
		return domain.ResourceObservation{}, err
	}
	if err = ctx.Err(); err != nil {
		return domain.ResourceObservation{}, err
	}
	end := time.Now()
	return domain.ResourceObservation{Resources: resources, ObservedAt: end.UTC(), Duration: end.Sub(start)}, nil
}
