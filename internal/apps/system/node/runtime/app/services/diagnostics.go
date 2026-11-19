package services

import (
	"context"
	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/domain"
	"time"
)

type DiagnosticsReader interface {
	Read(context.Context) (domain.Diagnostics, error)
}

func (service Service) Diagnostics(ctx context.Context) (domain.DiagnosticObservation, error) {
	if err := ctx.Err(); err != nil {
		return domain.DiagnosticObservation{}, err
	}
	start := time.Now()
	result, err := service.Probes.Read(ctx)
	if err != nil {
		return domain.DiagnosticObservation{}, err
	}
	if err = ctx.Err(); err != nil {
		return domain.DiagnosticObservation{}, err
	}
	healthy := result.HTTP != nil || result.GRPC != nil || result.NATS != nil
	if result.HTTP != nil {
		healthy = healthy && result.HTTP.Ready
	}
	if result.GRPC != nil {
		healthy = healthy && result.GRPC.Ready
	}
	if result.NATS != nil {
		healthy = healthy && result.NATS.ProbeSucceeded
	}
	end := time.Now()
	return domain.DiagnosticObservation{Diagnostics: result, Healthy: healthy, ObservedAt: end.UTC(), Duration: end.Sub(start)}, nil
}
