package services

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

// ReserveExecution borrows an execution for an operation spanning multiple
// native lifecycle calls. The caller must join those calls before releasing it.
func (s *Service) ReserveExecution(ctx context.Context, id, executionID string) (Runtime, func(), error) {
	entry, err := s.selectInstance(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	select {
	case entry.mutation <- struct{}{}:
	default:
		return nil, nil, domain.ErrBusy
	}
	release := func() { <-entry.mutation }
	runtime := entry.binding.Runtime
	switch {
	case s.closed.Load():
		err = domain.ErrClosed
	case ctx.Err() != nil:
		err = ctx.Err()
	case executionID == "" || runtime.Observe().ExecutionID != executionID:
		err = domain.ErrConflict
	}
	if err != nil {
		release()
		return nil, nil, err
	}
	return runtime, release, nil
}
