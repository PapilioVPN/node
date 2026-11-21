package services

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
)

func (s *StateService) Replace(ctx context.Context, id string, expected uint64, sources []domain.Source) (result domain.Change, err error) {
	ctx, entry, release, err := s.begin(ctx, id)
	if err != nil {
		return result, err
	}
	defer release()
	if s.checker == nil || s.restarter == nil {
		return result, domain.ErrUnsupported
	}
	metadata, err := entry.repository.Metadata(ctx)
	if err != nil {
		return result, err
	}
	result.Persistence.Metadata = metadata
	if metadata.Revision != expected {
		return result, domain.ErrConflict
	}
	validation, err := s.checker.CheckSources(ctx, id, sources)
	if err != nil {
		return result, err
	}
	if !validation.Valid {
		return result, errors.Join(domain.ErrRejected, validation.Failure)
	}
	result.Persistence, err = entry.repository.Write(ctx, expected, sources)
	if err != nil {
		return result, err
	}
	if !result.Persistence.Durable {
		return result, domain.ErrDurabilityUnknown
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	control, err := s.restarter.Restart(ctx, id)
	result.Execution = &control
	return result, executionError(err)
}

// Apply explicitly retries execution of the selected current desired revision.
// It does not republish a revision or promise idempotent process restarts.
func (s *StateService) Apply(ctx context.Context, id string, expected uint64) (result domain.Change, err error) {
	ctx, entry, release, err := s.begin(ctx, id)
	if err != nil {
		return result, err
	}
	defer release()
	if s.restarter == nil {
		return result, domain.ErrUnsupported
	}
	if expected == 0 {
		return result, domain.ErrNoDesired
	}
	metadata, err := entry.repository.Confirm(ctx, expected)
	result.Persistence.Metadata = metadata
	result.Persistence.Durable = metadata.Durable
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	control, err := s.restarter.Restart(ctx, id)
	result.Execution = &control
	return result, executionError(err)
}
