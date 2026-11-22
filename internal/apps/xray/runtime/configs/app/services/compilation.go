package services

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
)

type Compiler interface {
	Compile(context.Context, []domain.Source) (domain.Compilation, error)
}

func (s *Service) Compile(ctx context.Context, id string, sources []domain.Source) (domain.Compilation, error) {
	if err := ctx.Err(); err != nil {
		return domain.Compilation{}, err
	}
	if id == "" {
		return domain.Compilation{}, domain.ErrInvalidArgument
	}
	if err := validators.Sources(sources); err != nil {
		return domain.Compilation{}, err
	}
	return s.compile(ctx, id, sources)
}

// ValidatedCompiler is for local workflows that already checked source shape at
// entry or loaded validated sources from their repository. Native compilation,
// cancellation, instance selection and admission remain owned by this service.
func (s *Service) ValidatedCompiler() SourceCompiler { return validatedCompiler{s} }

type validatedCompiler struct{ service *Service }

func (c validatedCompiler) Compile(ctx context.Context, id string, sources []domain.Source) (domain.Compilation, error) {
	return c.service.compile(ctx, id, sources)
}
func (s *Service) compile(ctx context.Context, id string, sources []domain.Source) (domain.Compilation, error) {
	if err := ctx.Err(); err != nil {
		return domain.Compilation{}, err
	}
	binding, exists := s.bindings[id]
	if !exists {
		return domain.Compilation{}, domain.ErrNotFound
	}
	if binding.Compiler == nil {
		return domain.Compilation{}, domain.ErrUnsupported
	}
	start := time.Now()
	result, err := binding.Compiler.Compile(ctx, sources)
	if err != nil {
		return domain.Compilation{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.Compilation{}, err
	}
	result.InstanceID = id
	result.PreparedAt = time.Now().UTC()
	result.Duration = time.Since(start)
	return result, nil
}
