// Package validation checks Xray configuration with the native engine.
package validation

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray/configs/runconfig"
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

func (service *Service) Validate(ctx context.Context, req Request) (Result, error) {
	if req.ID == "" || req.ID != service.config.ID {
		return Result{}, ErrNotFound
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if len(req.ConfigJSON) > runconfig.MaxSourceBytes {
		return Result{}, runconfig.ErrSourceTooLarge
	}
	if !service.validating.CompareAndSwap(false, true) {
		return Result{}, ErrBusy
	}
	defer service.validating.Store(false)

	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	var issue *Issue
	var err error
	if req.ConfigJSON == nil {
		issue, err = service.run(ctx, service.config)
	} else {
		if !runconfig.ValidJSONSource(req.ConfigJSON) {
			return Result{ID: req.ID, Issue: &Issue{Code: DecodeError}}, nil
		}
		issue, err = CheckSource(ctx, service.config.Executable, service.config.RuntimeDirectory, req.ConfigJSON)
	}
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrFailed, err)
	}
	return Result{ID: req.ID, Issue: issue}, nil
}
