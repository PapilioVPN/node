package xray

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/PapilioVPN/node/internal/modules/instances/domain"
)

var ErrInvalidValidationSettings = errors.New("invalid Xray validation settings")

// ValidationConfig declares native execution bounds and operator-owned paths.
// Directory matches the instance's config directory for relative dependencies.
// WorkDir must already exist; only per-call private children are created/removed.
type ValidationConfig struct {
	Binary        string
	Directory     string
	WorkDir       string
	Timeout       time.Duration
	MaxBytes      int
	MaxConcurrent int
}

// Validator builds candidates through Xray's native convert pb command. Unlike
// run -test, this does not call core.New, which starts configured log handlers.
// It owns no runtime and never receives the instance's active configuration file.
type Validator struct {
	config ValidationConfig
	slots  chan struct{}
}

// NewValidator validates settings without filesystem I/O or native execution.
func NewValidator(config ValidationConfig) (*Validator, error) {
	if !filepath.IsAbs(config.Binary) || !filepath.IsAbs(config.Directory) || !filepath.IsAbs(config.WorkDir) || config.Timeout <= 0 || config.MaxBytes <= 0 || config.MaxConcurrent <= 0 {
		return nil, ErrInvalidValidationSettings
	}
	return &Validator{config: config, slots: make(chan struct{}, config.MaxConcurrent)}, nil
}

// ValidateConfig has immediate admission: saturated callers get ErrValidationBusy
// without queuing a retained candidate. One admitted call owns one native process,
// private candidate and execution timeout. It joins before releasing its slot.
// Native stdout/stderr and compiled output are discarded; Node buffers none.
// Filesystem calls are synchronous; cleanup is joined even after cancellation.
func (v *Validator) ValidateConfig(ctx context.Context, candidate domain.ConfigCandidate) (result domain.ConfigValidation, err error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	switch candidate.Format {
	case domain.JSON, domain.YAML, domain.TOML:
	default:
		return result, domain.ErrInvalidInput
	}
	if len(candidate.Content) == 0 {
		return result, domain.ErrInvalidInput
	}
	if len(candidate.Content) > v.config.MaxBytes {
		return result, domain.ErrConfigTooLarge
	}
	select {
	case v.slots <- struct{}{}:
		defer func() { <-v.slots }()
	default:
		return result, domain.ErrValidationBusy
	}
	ctx, cancel := context.WithTimeout(ctx, v.config.Timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	directory, err := os.MkdirTemp(v.config.WorkDir, "xray-validation-")
	if err != nil {
		return result, fmt.Errorf("create candidate directory: %w", err)
	}
	defer func() {
		cleanup := os.RemoveAll(directory)
		if cleanup != nil {
			cleanup = fmt.Errorf("remove candidate directory: %w", cleanup)
		}
		if cause := errors.Join(ctx.Err(), cleanup); cause != nil {
			err = errors.Join(err, result.Failure, cause)
			result = domain.ConfigValidation{}
		}
	}()
	path := filepath.Join(directory, "candidate."+string(candidate.Format))
	if err := os.WriteFile(path, candidate.Content, 0o600); err != nil {
		return result, fmt.Errorf("write candidate: %w", err)
	}
	command := exec.CommandContext(ctx, v.config.Binary, "convert", "pb", "-o", os.DevNull, path)
	command.Dir = v.config.Directory
	command.Env = []string{} // Match lifecycle execution: no inherited host overrides.
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	command.WaitDelay = v.config.Timeout
	// Nil stdin/stdout/stderr use native null files, not pipe-copy goroutines.
	err = command.Run()
	if cause := ctx.Err(); cause != nil {
		return result, errors.Join(cause, err)
	}
	if err == nil {
		return domain.ConfigValidation{Valid: true}, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		// Pinned convert pb uses base.Fatalf's exit 1 for config build rejection.
		return domain.ConfigValidation{Failure: err}, nil
	}
	return result, fmt.Errorf("build Xray candidate: %w", err)
}
