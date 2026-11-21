//go:build linux

package process

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/xtls/xray-core/core"
)

type sourceFiles struct {
	ctx       context.Context
	input     *os.File
	directory string
	arguments []string
	release   func()
}

func (f *sourceFiles) close() error {
	defer f.release()
	return errors.Join(f.input.Close(), os.RemoveAll(f.directory))
}

func (b *Builder) stageSources(ctx context.Context, sources []domain.Source) (*sourceFiles, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(sources) == 0 || len(sources) > domain.MaxSources {
		return nil, domain.ErrInvalidArgument
	}
	seen := make(map[string]bool, len(sources))
	for _, source := range sources {
		if source.Name == "" || filepath.Base(source.Name) != source.Name || strings.ContainsAny(source.Name, "\\\x00") || seen[source.Name] || core.GetFormat(source.Name) == "" {
			return nil, domain.ErrInvalidArgument
		}
		seen[source.Name] = true
	}
	ctx, release, err := b.admission.acquire(ctx)
	if err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(b.config.WorkDirectory, "sources-")
	if err != nil {
		release()
		return nil, err
	}
	failed := func(err error) (*sourceFiles, error) {
		cleanup := os.RemoveAll(directory)
		release()
		return nil, errors.Join(err, cleanup)
	}
	files := &sourceFiles{ctx: ctx, directory: directory, release: release}
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return failed(err)
		}
		if err := os.WriteFile(filepath.Join(directory, source.Name), source.Content, 0600); err != nil {
			return failed(err)
		}
		files.arguments = append(files.arguments, "/proc/self/fd/3/"+source.Name)
	}
	files.input, err = os.Open(directory)
	if err != nil {
		return failed(err)
	}
	return files, nil
}

// ValidateSources uses the native multi-file builder without exporting, decoding
// or hashing compiled output. Applying a valid source set must not depend on the
// separate public compilation-export byte ceiling.
func (b *Builder) ValidateSources(ctx context.Context, sources []domain.Source) (result domain.Validation, err error) {
	files, err := b.stageSources(ctx, sources)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, files.close()) }()
	command := exec.CommandContext(files.ctx, b.config.Binary, append([]string{"convert", "pb", "-o", "/dev/null"}, files.arguments...)...)
	command.Dir = b.config.Directory
	command.Env = []string{}
	command.WaitDelay = time.Second
	command.ExtraFiles = []*os.File{files.input}
	err = command.Run()
	if cause := files.ctx.Err(); cause != nil {
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
