package services

import (
	"context"
	"path"
	"strings"
	"time"
	"unicode/utf8"

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
	if err := validateSources(sources); err != nil {
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

func validateSources(sources []domain.Source) error {
	if len(sources) == 0 {
		return domain.ErrInvalidArgument
	}
	if len(sources) > domain.MaxSources {
		return domain.ErrTooLarge
	}
	seen := make(map[string]bool, len(sources))
	total := 0
	for _, source := range sources {
		name := source.Name
		if name == "" || len(name) > 128 || !utf8.ValidString(name) || strings.ContainsAny(name, "/\\\x00") || seen[name] {
			return domain.ErrInvalidArgument
		}
		seen[name] = true
		protobuf := false
		switch strings.ToLower(path.Ext(name)) {
		case ".json", ".jsonc", ".yaml", ".yml", ".toml":
		case ".pb", ".protobuf":
			protobuf = true
			if len(sources) != 1 {
				return domain.ErrInvalidArgument
			}
		default:
			return domain.ErrInvalidArgument
		}
		if len(source.Content) == 0 && !protobuf {
			return domain.ErrInvalidArgument
		}
		if len(source.Content) > domain.MaxCandidateBytes-total {
			return domain.ErrTooLarge
		}
		total += len(source.Content)
	}
	return nil
}
