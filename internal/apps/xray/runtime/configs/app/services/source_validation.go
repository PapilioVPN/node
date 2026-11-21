package services

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
)

type SourceBuilder interface {
	ValidateSources(context.Context, []domain.Source) (domain.Validation, error)
}

func (s *Service) CheckSources(ctx context.Context, id string, sources []domain.Source) (domain.Validation, error) {
	if err := ctx.Err(); err != nil {
		return domain.Validation{}, err
	}
	if id == "" {
		return domain.Validation{}, domain.ErrInvalidArgument
	}
	if err := validators.Sources(sources); err != nil {
		return domain.Validation{}, err
	}
	binding, exists := s.bindings[id]
	if !exists {
		return domain.Validation{}, domain.ErrNotFound
	}
	if binding.SourceBuilder == nil {
		return domain.Validation{}, domain.ErrUnsupported
	}
	start := time.Now()
	result, err := binding.SourceBuilder.ValidateSources(ctx, sources)
	if err != nil {
		return domain.Validation{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.Validation{}, err
	}
	result.InstanceID = id
	result.CheckedAt = time.Now().UTC()
	result.Duration = time.Since(start)
	return result, nil
}
