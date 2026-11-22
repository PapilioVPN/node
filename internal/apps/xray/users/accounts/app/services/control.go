package services

import (
	"context"
	"errors"
	"strings"
	"time"

	instancevalidators "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

func (s *Service) Add(ctx context.Context, id, tag string, content []byte) (domain.Operation, error) {
	if err := validators.Configuration(content); err != nil {
		return domain.Operation{InstanceID: id, InboundTag: tag, Kind: "add", Effect: domain.NotApplied}, err
	}
	return s.mutate(ctx, id, tag, "add", "", func(c Client) (domain.NativeResult, error) { return c.Add(ctx, tag, content) })
}
func (s *Service) Remove(ctx context.Context, id, tag, email string) (domain.Operation, error) {
	if err := validators.Email(email); err != nil {
		return domain.Operation{InstanceID: id, InboundTag: tag, Kind: "remove", Email: email, Effect: domain.NotApplied}, err
	}
	return s.mutate(ctx, id, tag, "remove", email, func(c Client) (domain.NativeResult, error) { return c.Remove(ctx, tag, email) })
}
func (s *Service) mutate(ctx context.Context, id, tag, kind, email string, invoke func(Client) (domain.NativeResult, error)) (operation domain.Operation, err error) {
	operation = domain.Operation{InstanceID: id, InboundTag: tag, Kind: kind, Email: email, Effect: domain.NotApplied}
	if err := ctx.Err(); err != nil {
		return operation, err
	}
	if instancevalidators.ID(id) != nil || validators.Selector(tag) != nil {
		return operation, domain.ErrInvalidArgument
	}
	selected := s.entries[id]
	if selected == nil {
		return operation, domain.ErrNotFound
	}
	if selected.client == nil {
		return operation, domain.ErrUnsupported
	}
	select {
	case s.mutations <- struct{}{}:
	default:
		return operation, domain.ErrCapacity
	}
	defer func() { <-s.mutations }()
	before, err := s.status.Status(ctx, id)
	if err != nil {
		return operation, err
	}
	operation.Generation = before.Execution.Generation
	if before.Execution.State != instances.Running {
		return operation, domain.ErrNotRunning
	}
	selected.mu.Lock()
	if selected.uncertainGeneration != 0 && selected.uncertainGeneration == operation.Generation {
		selected.mu.Unlock()
		operation.RequiresInstanceRestart = true
		return operation, domain.ErrUncertain
	}
	if selected.mutating || selected.readers != 0 {
		selected.mu.Unlock()
		return operation, domain.ErrCapacity
	}
	selected.mutating = true
	selected.mu.Unlock()
	started := time.Now()
	defer func() {
		operation.ObservedAt = time.Now().UTC()
		operation.Duration = time.Since(started)
		selected.mu.Lock()
		defer selected.mu.Unlock()
		selected.mutating = false
		if operation.RequiresInstanceRestart {
			selected.uncertainGeneration = operation.Generation
		}
		// Retained evidence must outlive transport-owned request buffers.
		copy := operation
		copy.InstanceID = strings.Clone(operation.InstanceID)
		copy.InboundTag = strings.Clone(operation.InboundTag)
		copy.Email = strings.Clone(operation.Email)

		selected.last = &copy
	}()
	if err := ctx.Err(); err != nil {
		return operation, err
	}
	result, nativeErr := invoke(selected.client)
	if kind == "add" {
		operation.Email = result.Email
	}
	operation.Effect = result.Effect
	operation.Acknowledged = result.Acknowledged
	operation.RequiresInstanceRestart = result.RequiresInstanceRestart
	after, statusErr := s.status.Status(ctx, id)
	if statusErr != nil {
		if operation.Acknowledged {
			operation.Effect = domain.Unknown
		}
		return operation, errors.Join(nativeErr, statusErr)
	}
	if after.Execution.State != instances.Running || after.Execution.Generation != operation.Generation {
		if operation.Effect != domain.NotApplied {
			operation.Effect = domain.Unknown
		}
		return operation, errors.Join(domain.ErrExecutionChanged, nativeErr)
	}
	return operation, nativeErr
}
func (s *Service) Status(ctx context.Context, id string) (domain.ControlStatus, error) {
	if err := ctx.Err(); err != nil {
		return domain.ControlStatus{}, err
	}
	if instancevalidators.ID(id) != nil {
		return domain.ControlStatus{}, domain.ErrInvalidArgument
	}
	selected := s.entries[id]
	if selected == nil {
		return domain.ControlStatus{}, domain.ErrNotFound
	}
	state, err := s.status.Status(ctx, id)
	if err != nil {
		return domain.ControlStatus{}, err
	}
	selected.mu.Lock()
	defer selected.mu.Unlock()
	result := domain.ControlStatus{InstanceID: id, Generation: state.Execution.Generation, ExecutionState: string(state.Execution.State), APIConfigured: selected.client != nil, RequestInFlight: selected.mutating, RequiresInstanceRestart: selected.uncertainGeneration != 0 && selected.uncertainGeneration == state.Execution.Generation, ObservedAt: time.Now().UTC()}
	if selected.last != nil {
		copy := *selected.last
		result.LastOperation = &copy
	}
	return result, nil
}
