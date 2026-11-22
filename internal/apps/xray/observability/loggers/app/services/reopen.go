package services

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

func (s *Service) Reopen(ctx context.Context, id string) (operation domain.Operation, err error) {
	operation = domain.Operation{InstanceID: id, Effect: domain.NotApplied}
	selected, err := s.selectEntry(ctx, id)
	if err != nil {
		return operation, err
	}
	if selected.restarter == nil {
		return operation, domain.ErrUnsupported
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return operation, domain.ErrCapacity
	}
	defer func() { <-s.slots }()
	before, err := s.status.Status(ctx, id)
	if err != nil {
		return operation, err
	}
	operation.Generation = before.Execution.Generation
	if before.Execution.State != instances.Running {
		return operation, domain.ErrNotRunning
	}
	selected.mu.Lock()
	if selected.active {
		selected.mu.Unlock()
		return operation, domain.ErrCapacity
	}
	if selected.uncertainGeneration != 0 && selected.uncertainGeneration == operation.Generation {
		operation.RequiresInstanceRestart = true
		selected.mu.Unlock()
		return operation, domain.ErrUncertain
	}
	selected.active = true
	selected.mu.Unlock()
	started := time.Now()
	defer func() {
		operation.ObservedAt = time.Now().UTC()
		operation.Duration = time.Since(started)
		selected.mu.Lock()
		defer selected.mu.Unlock()
		selected.active = false
		if operation.RequiresInstanceRestart {
			selected.uncertainGeneration = operation.Generation
		}
		// Retained evidence must outlive transport-owned request buffers.
		copy := operation
		copy.InstanceID = strings.Clone(operation.InstanceID)

		selected.last = &copy
	}()
	if err := ctx.Err(); err != nil {
		return operation, err
	}
	native, nativeErr := selected.restarter.Reopen(ctx)
	operation.Effect = native.Effect
	operation.Acknowledged = native.Acknowledged
	operation.RequiresInstanceRestart = native.RequiresInstanceRestart
	after, statusErr := s.status.Status(ctx, id)
	if statusErr != nil {
		if operation.Acknowledged {
			operation.Effect = domain.Unknown
		}
		return operation, errors.Join(nativeErr, statusErr)
	}
	if after.Execution.Generation != operation.Generation || after.Execution.State != instances.Running {
		if operation.Effect != domain.NotApplied {
			operation.Effect = domain.Unknown
		}
		return operation, errors.Join(domain.ErrExecutionChanged, nativeErr)
	}
	return operation, nativeErr
}
