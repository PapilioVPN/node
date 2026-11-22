package services

import (
	"context"
	"time"

	instancevalidators "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

func (s *Service) Add(ctx context.Context, id, tag string, content []byte) (domain.Operation, error) {
	if err := validators.Configuration(content); err != nil {
		return domain.Operation{InstanceID: id, InboundTag: tag, Kind: "add", Effect: domain.NotApplied}, err
	}
	return s.mutate(ctx, id, tag, "add", "", func(c Client, generation uint64) (domain.NativeResult, error) {
		return s.checkedAdd(ctx, c, id, tag, generation, content)
	})
}
func (s *Service) Remove(ctx context.Context, id, tag, email string) (domain.Operation, error) {
	if err := validators.Email(email); err != nil {
		return domain.Operation{InstanceID: id, InboundTag: tag, Kind: "remove", Email: email, Effect: domain.NotApplied}, err
	}
	return s.mutate(ctx, id, tag, "remove", email, func(c Client, generation uint64) (domain.NativeResult, error) { return c.Remove(ctx, tag, email) })
}
func (s *Service) mutate(ctx context.Context, id, tag, kind, email string, invoke func(Client, uint64) (domain.NativeResult, error)) (operation domain.Operation, err error) {
	operation = domain.Operation{InstanceID: id, InboundTag: tag, Kind: kind, Email: email, Effect: domain.NotApplied}
	if err := ctx.Err(); err != nil {
		return operation, err
	}
	if instancevalidators.ID(id) != nil || validators.Selector(tag) != nil {
		return operation, domain.ErrInvalidArgument
	}
	reservation, err := s.reserveMutation(ctx, id, 1)
	operation.Generation = reservation.generation
	operation.RequiresInstanceRestart = reservation.requiresRestart
	if err != nil {
		return operation, err
	}
	defer reservation.finish(&operation)

	if err := ctx.Err(); err != nil {
		return operation, err
	}
	result, nativeErr := invoke(reservation.selected.client, operation.Generation)
	if kind == "add" {
		operation.Email = result.Email
	}
	operation.Guidance = result.Guidance
	operation.Effect = result.Effect
	operation.Acknowledged = result.Acknowledged
	operation.RequiresInstanceRestart = result.RequiresInstanceRestart
	err = s.observeMutation(ctx, &operation, nativeErr)
	if operation.Guidance == nil {
		operation.Guidance = guidance(err, operation.Effect)
	}
	return operation, err
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
		copy := cloneOperation(*selected.last)
		result.LastOperation = &copy
	}
	return result, nil
}
