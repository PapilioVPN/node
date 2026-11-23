// Package validation checks the configured Xray file with the native engine.
package validation

import (
	"context"
	"fmt"
	"time"
)

type Config struct {
	ID               string
	Executable       string
	ConfigFile       string
	RuntimeDirectory string
}

type Service struct {
	config Config
	slot   chan struct{}
	run    func(context.Context, Config) (*Issue, error)
}

func New(config Config) *Service {
	return &Service{config: config, slot: make(chan struct{}, 1), run: runNative}
}

func (service *Service) Validate(ctx context.Context, id string) (Result, error) {
	if id == "" || id != service.config.ID {
		return Result{}, ErrNotFound
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	select {
	case service.slot <- struct{}{}:
		defer func() { <-service.slot }()
	default:
		return Result{}, ErrBusy
	}

	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	issue, err := service.run(ctx, service.config)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrFailed, err)
	}
	return Result{ID: id, Issue: issue}, nil
}
