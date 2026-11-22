package services

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"golang.org/x/sync/errgroup"
)

func (s *Service) AddMany(ctx context.Context, id string, items []domain.Addition, parallelism int) (domain.Batch, error) {
	if err := validators.Additions(items); err != nil {
		return domain.Batch{}, err
	}
	ops := make([]domain.Operation, len(items))
	for i, item := range items {
		ops[i] = domain.Operation{InstanceID: id, InboundTag: item.InboundTag, Kind: "add", Effect: domain.NotApplied}
	}
	return s.batch(ctx, id, "add_batch", ops, parallelism, func(results []domain.ItemResult) error { return s.checkBatch(ctx, id, items, results) }, func(c Client, i int) (domain.NativeResult, error) {
		return c.Add(ctx, items[i].InboundTag, items[i].Configuration)
	})
}

func (s *Service) RemoveMany(ctx context.Context, id string, items []domain.Removal, parallelism int) (domain.Batch, error) {
	if err := validators.Removals(items); err != nil {
		return domain.Batch{}, err
	}
	ops := make([]domain.Operation, len(items))
	for i, item := range items {
		ops[i] = domain.Operation{InstanceID: id, InboundTag: item.InboundTag, Email: item.Email, Kind: "remove", Effect: domain.NotApplied}
	}
	return s.batch(ctx, id, "remove_batch", ops, parallelism, nil, func(c Client, i int) (domain.NativeResult, error) {
		return c.Remove(ctx, items[i].InboundTag, items[i].Email)
	})
}

func (s *Service) batch(ctx context.Context, id, kind string, operations []domain.Operation, parallelism int, prepare func([]domain.ItemResult) error, invoke func(Client, int) (domain.NativeResult, error)) (result domain.Batch, err error) {
	result.Operation = domain.Operation{InstanceID: id, Kind: kind, Effect: domain.NotApplied}
	result.Items = make([]domain.ItemResult, len(operations))
	for i, operation := range operations {
		result.Items[i] = domain.ItemResult{Index: i, Operation: operation, Err: domain.ErrNotAttempted}
	}
	if parallelism < 0 || parallelism > domain.MaxBatchParallelism {
		return domain.Batch{}, domain.ErrInvalidArgument
	}
	if parallelism == 0 {
		parallelism = domain.MaxBatchParallelism
	}
	parallel := min(parallelism, len(operations))
	reservation, err := s.reserveMutation(ctx, id, parallel)
	result.Operation.Generation = reservation.generation
	result.Operation.RequiresInstanceRestart = reservation.requiresRestart
	for i := range result.Items {
		result.Items[i].Operation.Generation = reservation.generation
	}
	if err != nil {
		return result, err
	}
	defer reservation.finish(&result.Operation)
	if prepare != nil {
		if err := prepare(result.Items); err != nil {
			if errors.Is(err, domain.ErrUncertain) {
				result.Operation.RequiresInstanceRestart = true
			}
			for i := range result.Items {
				result.Items[i].Err = err
				result.Items[i].Operation.Guidance = guidance(err, domain.NotApplied)
			}
			return result, err
		}
	}
	var stopped atomic.Bool
	var running errgroup.Group
	running.SetLimit(parallel)
	for position := range result.Items {
		if stopped.Load() {
			break
		}
		running.Go(func() error {
			if stopped.Load() {
				return nil
			}
			item := &result.Items[position]
			if !errors.Is(item.Err, domain.ErrNotAttempted) {
				stopped.Store(true)
				return nil
			}
			// Local observations add no native preflight RPC. Check execution
			// before each submission, not just the beginning of a long batch.
			if cause := s.observeMutation(ctx, &item.Operation, ctx.Err()); cause != nil {
				item.Err = cause
				stopped.Store(true)
				return nil
			}
			started := time.Now()
			item.Attempted = true
			native, cause := invoke(reservation.selected.client, position)
			if cause != nil {
				stopped.Store(true)
			}
			item.Operation.Guidance = native.Guidance
			item.Operation.Effect = native.Effect
			item.Operation.Acknowledged = native.Acknowledged
			item.Operation.RequiresInstanceRestart = native.RequiresInstanceRestart
			if item.Operation.Kind == "add" {
				item.Operation.Email = native.Email
			}
			item.Err = s.observeMutation(ctx, &item.Operation, cause)
			item.Operation.ObservedAt = time.Now().UTC()
			item.Operation.Duration = time.Since(started)
			if item.Err != nil {
				stopped.Store(true)
			}
			return nil
		})
	}

	_ = running.Wait() // Every started native call is joined, including on failure.
	completed, unknown := 0, false
	for i := range result.Items {
		item := &result.Items[i]
		if item.Err != nil && item.Operation.Guidance == nil {
			item.Operation.Guidance = guidance(item.Err, item.Operation.Effect)
		}
		if item.Operation.RequiresInstanceRestart && item.Operation.Guidance != nil {
			item.Operation.Guidance.NextAction = "replace_execution_before_replay"
		}
		if !errors.Is(item.Err, domain.ErrNotAttempted) {
			err = errors.Join(err, item.Err)
		}
		if item.Operation.Effect == domain.Completed {
			completed++
		}
		unknown = unknown || item.Operation.Effect == domain.Unknown
		result.Operation.RequiresInstanceRestart = result.Operation.RequiresInstanceRestart || item.Operation.RequiresInstanceRestart
	}
	switch {
	case unknown:
		result.Operation.Effect = domain.Unknown
	case completed == len(result.Items):
		result.Operation.Effect = domain.Completed
		result.Operation.Acknowledged = true
	case completed != 0:
		result.Operation.Effect = domain.Partial
	}
	return result, err
}
