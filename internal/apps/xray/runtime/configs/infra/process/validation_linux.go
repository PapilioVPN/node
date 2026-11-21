//go:build linux

// Package process runs the selected Xray configuration builder in its own context.
package process

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
)

type Config struct {
	Binary, Directory, WorkDirectory string
	MaxOutputBytes                   int
}
type Builder struct {
	config    Config
	admission *Admission
}

func New(config Config, admission *Admission) (*Builder, error) {
	if config.MaxOutputBytes == 0 {
		config.MaxOutputBytes = domain.MaxCompiledBytes
	}
	if !filepath.IsAbs(config.Binary) || !filepath.IsAbs(config.Directory) || !filepath.IsAbs(config.WorkDirectory) || admission == nil {
		return nil, domain.ErrInvalidArgument
	}
	if config.MaxOutputBytes < 0 || config.MaxOutputBytes > domain.MaxCompiledBytes {
		return nil, domain.ErrInvalidArgument
	}
	return &Builder{config: config, admission: admission}, nil
}

// Validate calls convert pb, which loads/builds without core.New. In particular,
// run -test would construct engine features and open configured log files.
// Input is borrowed only until return. Every exit joins the converter and removes
// its private candidate; native asset reads/allocations are not a sandbox.
func (b *Builder) Validate(ctx context.Context, candidate domain.Candidate) (result domain.Validation, err error) {
	if err = ctx.Err(); err != nil {
		return result, err
	}
	var extension string
	switch candidate.Format {
	case domain.JSON:
		extension = "json"
	case domain.YAML:
		extension = "yaml"
	case domain.TOML:
		extension = "toml"
	case domain.Protobuf:
		extension = "pb"
	default:
		return result, domain.ErrInvalidArgument
	}
	ctx, release, err := b.admission.acquire(ctx)
	if err != nil {
		return result, err
	}
	defer release()
	directory, err := os.MkdirTemp(b.config.WorkDirectory, "candidate-")
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(directory)) }()
	path := filepath.Join(directory, "config."+extension)
	if err = os.WriteFile(path, candidate.Content, 0600); err != nil {
		return result, err
	}
	command := exec.CommandContext(ctx, b.config.Binary, "convert", "pb", "-o", "/dev/null", path)
	command.Dir = b.config.Directory
	command.Env = []string{}
	command.WaitDelay = time.Second
	// Nil outputs use the null device, never a pipe or retained diagnostics.
	err = command.Run()
	if cause := ctx.Err(); cause != nil {
		return result, errors.Join(cause, err)
	}
	if err == nil {
		result.Valid = true
		return result, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		result.Failure = err
		return result, nil
	}
	return result, err
}
