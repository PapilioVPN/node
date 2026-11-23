// Package validation checks the configured Xray file with the native engine.
package validation

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
)

type Config struct {
	ID               string
	Executable       string
	ConfigFile       string
	RuntimeDirectory string
}

type Service struct {
	config     Config
	validating atomic.Bool
	run        func(context.Context, Config) (*Issue, error)
}

func New(config Config) *Service {
	return &Service{config: config, run: runNative}
}

func (service *Service) Validate(ctx context.Context, id string) (Result, error) {
	if id == "" || id != service.config.ID {
		return Result{}, ErrNotFound
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if !service.validating.CompareAndSwap(false, true) {
		return Result{}, ErrBusy
	}
	defer service.validating.Store(false)

	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	issue, err := service.run(ctx, service.config)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrFailed, err)
	}
	return Result{ID: id, Issue: issue}, nil
}
