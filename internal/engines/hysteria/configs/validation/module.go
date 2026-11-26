// Package validation checks the proposed or configured Hysteria YAML source.
package validation

import (
	"context"

	"github.com/PapilioVPN/node/internal/engines/hysteria/configs/preflight"
	"github.com/PapilioVPN/node/internal/engines/hysteria/internal/fault"
)

type Request struct {
	ID     string
	Source []byte
}

type Result struct {
	ID    string
	Issue preflight.Issue
}

type Service struct {
	sources map[string]preflight.Source
	slots   chan struct{}
}

func New(sources map[string]preflight.Source) *Service {
	return &Service{sources: sources, slots: make(chan struct{}, 4)}
}

func (s *Service) ValidateConfig(ctx context.Context, req Request) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if req.ID == "" {
		return Result{}, fault.ErrInvalid
	}
	source, ok := s.sources[req.ID]
	if !ok {
		return Result{}, fault.ErrNotFound
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return Result{}, fault.ErrBusy
	}
	data, err := source.Read(ctx, req.Source)
	if err != nil {
		return Result{}, err
	}
	_, issue, err := source.Check(ctx, data)
	if err != nil {
		return Result{}, err
	}
	return Result{ID: req.ID, Issue: issue}, nil
}
