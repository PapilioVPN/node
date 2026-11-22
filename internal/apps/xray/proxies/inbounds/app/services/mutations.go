package services

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

func (s *Service) Add(ctx context.Context, id string, content []byte) (domain.Operation, error) {
	if err := validators.Configuration(content); err != nil {
		return domain.Operation{InstanceID: id, Kind: "add", Effect: domain.NotApplied}, err
	}
	return s.mutate(ctx, id, "add", "", func(client Client, _ uint64) (domain.NativeResult, error) {
		return client.Add(ctx, content)
	})
}
func (s *Service) Remove(ctx context.Context, id, tag string) (domain.Operation, error) {
	if err := validators.Tag(tag); err != nil {
		return domain.Operation{InstanceID: id, Kind: "remove", Tag: tag, Effect: domain.NotApplied}, err
	}
	return s.mutate(ctx, id, "remove", tag, func(client Client, _ uint64) (domain.NativeResult, error) { return client.Remove(ctx, tag) })
}

// Both native mutations share actual manager admission and outcome semantics.
// No mutex is held over native I/O. Reads do not queue behind native mutation
// locks, whose file/socket operations may ignore RPC cancellation.
func (s *Service) mutate(ctx context.Context, id, kind, tag string, invoke func(Client, uint64) (domain.NativeResult, error)) (operation domain.Operation, err error) {
	operation = domain.Operation{InstanceID: id, Kind: kind, Tag: tag, Effect: domain.NotApplied}
	selected, err := s.selectEntry(ctx, id)
	if err != nil {
		return operation, err
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
	selected.epoch++
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
		copy := cloneOperation(operation)
		copy.InstanceID = strings.Clone(operation.InstanceID)
		copy.Tag = strings.Clone(operation.Tag)

		selected.last = &copy
	}()
	if err := ctx.Err(); err != nil {
		return operation, err
	}
	native, nativeErr := invoke(selected.client, operation.Generation)
	if kind == "add" || kind == "replace" {
		operation.Tag = native.Tag
	}
	operation.Replacement = native.Replacement
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
