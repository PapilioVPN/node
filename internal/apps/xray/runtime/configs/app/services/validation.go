// Package services coordinates Xray source preparation and desired-state changes.
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
	ID            string
	Builder       Builder
	Compiler      Compiler
	SourceBuilder SourceBuilder
}

// Service owns an immutable instance index. Native builders own their effects.
type Service struct{ bindings map[string]Binding }

func New(bindings []Binding) (*Service, error) {
	s := &Service{bindings: make(map[string]Binding, len(bindings))}
	for _, binding := range bindings {
		if binding.ID == "" {
			return nil, domain.ErrInvalidArgument
		}
		if _, exists := s.bindings[binding.ID]; exists {
			return nil, domain.ErrInvalidArgument
		}
		s.bindings[binding.ID] = binding
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
	binding, exists := s.bindings[id]
	if !exists {
		return domain.Validation{}, domain.ErrNotFound
	}
	if binding.Builder == nil {
		return domain.Validation{}, domain.ErrUnsupported
	}
	start := time.Now()
	result, err := binding.Builder.Validate(ctx, candidate)
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
