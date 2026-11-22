package services

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

func (s *Service) beginRead(ctx context.Context, id string) (*entry, uint64, func(), error) {
	selected, err := s.selectEntry(ctx, id)
	if err != nil {
		return nil, 0, nil, err
	}
	if selected.client == nil {
		return nil, 0, nil, domain.ErrUnsupported
	}
	before, err := s.status.Status(ctx, id)
	if err != nil {
		return nil, 0, nil, err
	}
	if before.Execution.State != instances.Running {
		return nil, 0, nil, domain.ErrNotRunning
	}
	select {
	case s.reads <- struct{}{}:
	default:
		return nil, 0, nil, domain.ErrCapacity
	}
	selected.mu.Lock()
	if selected.uncertainGeneration != 0 && selected.uncertainGeneration == before.Execution.Generation {
		selected.mu.Unlock()
		<-s.reads
		return nil, 0, nil, domain.ErrUncertain
	}
	if selected.mutating || selected.readers >= 8 {
		selected.mu.Unlock()
		<-s.reads
		return nil, 0, nil, domain.ErrCapacity
	}
	selected.readers++
	selected.mu.Unlock()
	return selected, before.Execution.Generation, func() { selected.mu.Lock(); selected.readers--; selected.mu.Unlock(); <-s.reads }, nil
}
func (s *Service) finishRead(ctx context.Context, id string, generation uint64, cause error) error {
	after, err := s.status.Status(ctx, id)
	if err != nil {
		return errors.Join(cause, err)
	}
	if after.Execution.Generation != generation || after.Execution.State != instances.Running {
		return errors.Join(domain.ErrExecutionChanged, cause)
	}
	return errors.Join(cause, ctx.Err())
}
func (s *Service) Records(ctx context.Context, id string) (domain.Records, error) {
	selected, generation, release, err := s.beginRead(ctx, id)
	if err != nil {
		return domain.Records{}, err
	}
	defer release()
	started := time.Now()
	records, err := selected.client.Records(ctx)
	if err = s.finishRead(ctx, id, generation, err); err != nil {
		return domain.Records{}, err
	}
	return domain.Records{InstanceID: id, Generation: generation, Outbounds: records, ObservedAt: time.Now().UTC(), Duration: time.Since(started)}, nil
}
