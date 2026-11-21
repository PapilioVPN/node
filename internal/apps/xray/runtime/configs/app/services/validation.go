// Package services coordinates candidate checks for configured Xray instances.
package services

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
)

type Builder interface {
	Validate(context.Context, domain.Candidate) (domain.Validation, error)
}

type Binding struct {
	ID      string
	Builder Builder
}

// Service owns an immutable instance index. Native builders own their effects.
type Service struct{ builders map[string]Builder }

func New(bindings []Binding) (*Service, error) {
	s := &Service{builders: make(map[string]Builder, len(bindings))}
	for _, binding := range bindings {
		if binding.ID == "" {
			return nil, domain.ErrInvalidArgument
		}
		if _, exists := s.builders[binding.ID]; exists {
			return nil, domain.ErrInvalidArgument
		}
		s.builders[binding.ID] = binding.Builder
	}
	return s, nil
}

func (s *Service) Validate(ctx context.Context, id string, candidate domain.Candidate) (domain.Validation, error) {
	if err := ctx.Err(); err != nil {
		return domain.Validation{}, err
	}
	if id == "" {
		return domain.Validation{}, domain.ErrInvalidArgument
	}
	if err := validators.Candidate(candidate); err != nil {
		return domain.Validation{}, err
	}
	builder, exists := s.builders[id]
	if !exists {
		return domain.Validation{}, domain.ErrNotFound
	}
	if builder == nil {
		return domain.Validation{}, domain.ErrUnsupported
	}
	start := time.Now()
	result, err := builder.Validate(ctx, candidate)
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
