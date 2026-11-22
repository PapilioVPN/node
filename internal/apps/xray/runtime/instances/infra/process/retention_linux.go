//go:build linux

package process

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

// SetRetention explicitly preserves this execution on the next clean Node stop.
// Abrupt Node exit is recovered independently when private state is configured.
func (p *Process) SetRetention(ctx context.Context, id string, preserve bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.config.RecoveryDirectory == "" {
		return domain.ErrUnsupported
	}
	if !p.mutation.TryLock() {
		return domain.ErrBusy
	}
	defer p.mutation.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return domain.ErrClosed
	}
	if len(id) != 32 || p.snapshot.ExecutionID != id || p.snapshot.State != domain.Running {
		return domain.ErrConflict
	}
	p.retain = preserve
	p.snapshot.PreserveOnClose = preserve
	return nil
}
func (p *Process) release(ctx context.Context) error {
	p.mu.Lock()
	run := p.current
	p.mu.Unlock()
	if run != nil {
		run.cancel()
		select {
		case <-run.watchDone:
		case <-ctx.Done():
			return ctx.Err()
		}
		if run.command != nil {
			run.command.Process.Release()
		}
	}
	p.mu.Lock()
	output := p.lastOutput
	p.mu.Unlock()
	var err error
	if output != nil && output.output != nil {
		err = closeOutput(output.output)

	}
	return errors.Join(err, p.api.close(), p.records.close())
}
