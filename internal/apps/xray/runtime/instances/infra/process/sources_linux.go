//go:build linux

package process

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

// Source opens the exact original sources for a managed execution. A nil input
// selects the operator's baseline configuration. Release owns staged files too.
type Source interface {
	Open(context.Context) (*Input, error)
}
type SourceFactory interface{ ForInstance(string) Source }
type Input struct {
	Directory *os.File
	Names     []string
	Revision  uint64
	Release   func() error
}

func validateInput(input *Input) error {
	if input == nil {
		return nil
	}
	if input.Directory == nil || len(input.Names) == 0 || input.Revision == 0 {
		return domain.ErrInvalidArgument
	}
	for _, name := range input.Names {
		if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, "\\\x00") {
			return domain.ErrInvalidArgument
		}
	}
	return nil
}

func (p *Process) cleanupInput(input *Input) error {
	if input == nil {
		return nil
	}
	var err error
	if input.Directory != nil {
		err = input.Directory.Close()
		if errors.Is(err, os.ErrClosed) {
			err = nil
		}
	}
	if input.Release != nil {
		err = errors.Join(err, input.Release())
	}
	return err
}

func (p *Process) cleanupPending(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	input := p.pending
	p.mu.Unlock()
	if input == nil {
		return nil
	}
	err := p.cleanupInput(input)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.snapshot.CleanupFailure = err
	if err == nil {
		p.pending = nil
		return nil
	}
	return errors.Join(domain.ErrCleanupFailed, err)
}

func (p *Process) discardInput(input *Input) error {
	err := p.cleanupInput(input)
	if err != nil {
		p.mu.Lock()
		p.pending = input
		p.snapshot.CleanupFailure = err
		p.mu.Unlock()
		return errors.Join(domain.ErrCleanupFailed, err)
	}
	return nil
}
