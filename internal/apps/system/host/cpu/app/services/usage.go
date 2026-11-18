package services

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/app/policies"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
)

// SampleUsage owns two independent native samples and one cancelable timer.
// Raw SDK handles are closed before waiting. No global baseline or worker is used.
func (service Service) SampleUsage(ctx context.Context, input domain.UsageRequest) (domain.UsageObservation, error) {
	if input.Interval <= 0 {
		return domain.UsageObservation{}, domain.ErrInvalidSampleInterval
	}
	if err := ctx.Err(); err != nil {
		return domain.UsageObservation{}, err
	}
	started := time.Now()
	before, err := service.TimesReader.Read(ctx, input.PerCPU)
	if err != nil {
		return domain.UsageObservation{}, err
	}
	sampled := time.Now()
	timer := time.NewTimer(input.Interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return domain.UsageObservation{}, ctx.Err()
	case <-timer.C:
	}
	if err := ctx.Err(); err != nil {
		return domain.UsageObservation{}, err
	}
	after, err := service.TimesReader.Read(ctx, input.PerCPU)
	if err != nil {
		return domain.UsageObservation{}, err
	}
	completed := time.Now()
	if err := ctx.Err(); err != nil {
		return domain.UsageObservation{}, err
	}
	usages, err := policies.Usage(before, after)
	if err != nil {
		return domain.UsageObservation{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.UsageObservation{}, err
	}
	return domain.UsageObservation{PerCPU: input.PerCPU, TicksPerSecond: after.TicksPerSecond, CPUs: usages, RequestedInterval: input.Interval, StartedAt: sampled.UTC(), SampleInterval: completed.Sub(sampled), ObservedAt: completed.UTC(), Duration: time.Since(started)}, nil
}
