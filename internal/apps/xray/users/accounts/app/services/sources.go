package services

import (
	"context"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

type SourcePreparer interface {
	Prepare(context.Context, string, string, string, []configs.Source) ([]byte, error)
	PrepareMany(context.Context, string, []domain.SourceSelection, []configs.Source) ([]domain.SourceAccount, error)
}

// AddMany prepares the complete source collection once inside the existing live
// batch reservation. Selections and results retain input order and duplicates.
func (s *Sources) AddMany(ctx context.Context, id string, items []domain.SourceSelection, sources []configs.Source, parallelism int) (domain.Batch, error) {
	if err := validators.SourceSelections(items); err != nil {
		return domain.Batch{}, err
	}
	operations := make([]domain.Operation, len(items))
	for i, item := range items {
		operations[i] = domain.Operation{InstanceID: id, InboundTag: item.InboundTag, Email: item.Email, Kind: "add", Effect: domain.NotApplied}
	}
	var prepared []domain.SourceAccount
	return s.service.batch(ctx, id, "add_batch", operations, parallelism, func(results []domain.ItemResult) error {
		var err error
		prepared, err = s.preparer.PrepareMany(ctx, id, items, sources)
		if err != nil {
			return err
		}
		if len(prepared) != len(items) {
			return domain.ErrInvalidArgument
		}
		for i, account := range prepared {
			if account.Err != nil {
				results[i].Err = account.Err
			}
		}
		return ctx.Err()
	}, func(client Client, i int) (domain.NativeResult, error) {
		return client.Add(ctx, items[i].InboundTag, prepared[i].Configuration)
	})
}

type Sources struct {
	service  *Service
	preparer SourcePreparer
}

func NewSources(service *Service, preparer SourcePreparer) (*Sources, error) {
	if service == nil || preparer == nil {
		return nil, domain.ErrInvalidArgument
	}
	return &Sources{service, preparer}, nil
}

// Add shares the existing mutation admission and receipt owner. Compilation
// failure and replacement before submission apply no native account mutation.
func (s *Sources) Add(ctx context.Context, id, tag, email string, sources []configs.Source) (domain.Operation, error) {
	if err := validators.Email(email); err != nil {
		return domain.Operation{InstanceID: id, InboundTag: tag, Kind: "add", Email: email, Effect: domain.NotApplied}, err
	}
	return s.service.mutate(ctx, id, tag, "add", email, func(client Client, generation uint64) (domain.NativeResult, error) {
		content, err := s.preparer.Prepare(ctx, id, tag, email, sources)
		if err != nil {
			return domain.NativeResult{Email: email, Effect: domain.NotApplied}, err
		}
		if err := ctx.Err(); err != nil {
			return domain.NativeResult{Email: email, Effect: domain.NotApplied}, err
		}
		// Generation checks fence local execution changes; this is not a transaction
		// with external inbound replacement or an attestation of remote endpoint type.
		state, err := s.service.status.Status(ctx, id)
		if err != nil {
			return domain.NativeResult{Email: email, Effect: domain.NotApplied}, err
		}
		if state.Execution.State != instances.Running || state.Execution.Generation != generation {
			return domain.NativeResult{Email: email, Effect: domain.NotApplied}, domain.ErrExecutionChanged
		}
		return client.Add(ctx, tag, content)
	})
}
