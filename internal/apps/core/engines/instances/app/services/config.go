package services

import (
	"context"
	"errors"
	"maps"

	"github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
)

// ConfigValidator builds a candidate without applying it or controlling execution.
// Each adapter owns its native formats, explicit work limits and resource cleanup.
type ConfigValidator interface {
	ValidateConfig(context.Context, domain.ConfigCandidate) (domain.ConfigValidation, error)
}

// ConfigService selects an existing instance's optional configuration capability.
// A nil validator denotes a configured instance with validation disabled.
type ConfigService struct {
	validators map[domain.InstanceID]ConfigValidator
}

func NewConfigService(validators map[domain.InstanceID]ConfigValidator) *ConfigService {
	return &ConfigService{validators: maps.Clone(validators)}
}

func (s *ConfigService) ValidateInstanceConfig(ctx context.Context, id domain.InstanceID, candidate domain.ConfigCandidate) (domain.ConfigValidation, error) {
	if err := ctx.Err(); err != nil {
		return domain.ConfigValidation{}, err
	}
	validator, exists := s.validators[id]
	if !exists {
		return domain.ConfigValidation{}, domain.ErrNotFound
	}
	if validator == nil {
		return domain.ConfigValidation{}, domain.ErrValidationDisabled
	}
	result, err := validator.ValidateConfig(ctx, candidate)
	if cause := ctx.Err(); cause != nil {
		return domain.ConfigValidation{}, errors.Join(cause, err, result.Failure)
	}
	if err != nil {
		return domain.ConfigValidation{}, err
	}
	return result, nil
}
