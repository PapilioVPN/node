// Package process owns Xray runtimes and bounded native commands on Linux.
package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/PapilioVPN/node/internal/modules/core/instances/domain"
)

var ErrInvalidConfig = errors.New("invalid Xray process settings")

var ErrClosed = errors.New("Xray process owner is closed")

// Config contains explicit process inputs. Xray owns parsing its config file.
// StopTimeout bounds graceful shutdown and native exec pipe cleanup.
type Config struct {
	Binary      string
	ConfigFile  string
	StopTimeout time.Duration
}

// Process owns one Xray execution at a time. Its zero value is not usable.
// Reuse it for manual stop/start; it does not automatically restart failed runs.
type Process struct {
	config  Config
	control chan struct{} // Cancelable admission to conflicting lifecycle mutations.
	closed  bool          // Protected by control; final Close prevents future starts.
	mu      sync.Mutex    // Protects observations only; never held across OS I/O.
	state   domain.State
	run     *execution
}

type execution struct {
	command *exec.Cmd
	cancel  context.CancelFunc
	done    chan struct{}
	err     error // Published by closing done; immutable afterwards.
}

// New validates explicit inputs without reading files or launching a process.
func New(config Config) (*Process, error) {
	if !filepath.IsAbs(config.Binary) || !filepath.IsAbs(config.ConfigFile) || config.StopTimeout <= 0 {
		return nil, ErrInvalidConfig
	}
	return &Process{config: config, control: make(chan struct{}, 1), state: domain.Stopped}, nil
}

// Start is idempotent while the owned execution is running. Context controls
// admission; native OS process creation is synchronous. Once launched, the child
// survives the request context and remains owned until Stop, Close or exit.
// Running means successfully spawned, not validated config or service readiness.
func (p *Process) Start(ctx context.Context) error {
	if err := p.acquire(ctx); err != nil {
		return err
	}
	defer p.release()
	if p.closed {
		return ErrClosed
	}
	p.mu.Lock()
	previous := p.run
	p.mu.Unlock()
	if previous != nil {
		select {
		case <-previous.done:
		default:
			return nil
		}
	}

	lifetime, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(lifetime, p.config.Binary, "run", "-config", p.config.ConfigFile)
	command.Dir = filepath.Dir(p.config.ConfigFile)
	command.Env = []string{} // Never inherit host Xray configuration overrides.
	command.Stdout, command.Stderr = io.Discard, io.Discard
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	command.WaitDelay = p.config.StopTimeout
	if err := command.Start(); err != nil {
		cancel()
		return fmt.Errorf("start Xray: %w", err)
	}
	run := &execution{command: command, cancel: cancel, done: make(chan struct{})}
	p.mu.Lock()
	p.run, p.state = run, domain.Running
	p.mu.Unlock()
	go p.wait(lifetime, run)
	return nil
}

// Stop requests SIGTERM and joins the execution. The earlier of the caller's
// deadline and StopTimeout forces termination; its cause remains inspectable.
// Cancellation before admission leaves cleanup to the resource owner's Close.
func (p *Process) Stop(ctx context.Context) error {
	if err := p.acquire(ctx); err != nil {
		return err
	}
	defer p.release()
	return p.stop(ctx)
}

// stop runs with lifecycle mutation ownership already acquired.
func (p *Process) stop(ctx context.Context) error {
	p.mu.Lock()
	run := p.run
	if run == nil {
		p.mu.Unlock()
		return nil
	}
	select {
	case <-run.done:
		p.mu.Unlock()
		return run.err
	default:
		p.state = domain.Stopping
	}
	p.mu.Unlock()
	deadline, cancel := context.WithTimeout(ctx, p.config.StopTimeout)
	defer cancel()
	run.cancel()
	select {
	case <-run.done:
		return errors.Join(deadline.Err(), run.err)
	case <-deadline.Done():
		err := run.command.Process.Kill()
		if errors.Is(err, os.ErrProcessDone) {
			err = nil
		}
		<-run.done
		return errors.Join(deadline.Err(), err, run.err)
	}
}

// Close stops admission permanently and joins the owned execution, including
// after an expired Stop context. Only Stop permits a later manual restart.
func (p *Process) Close() error {
	// Final ownership cleanup cannot inherit a canceled request's admission budget.
	_ = p.acquire(context.Background())
	defer p.release()
	p.closed = true
	return p.stop(context.Background())
}

// Observe returns the cached lifecycle state and completed execution's native
// cause. It performs no OS/protocol I/O and does not infer readiness.
func (p *Process) Observe() (domain.State, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.run != nil {
		select {
		case <-p.run.done:
			return p.state, p.run.err
		default:
		}
	}
	return p.state, nil
}

func (p *Process) acquire(ctx context.Context) error {
	select {
	case p.control <- struct{}{}:
		if err := ctx.Err(); err != nil {
			p.release()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Process) release() { <-p.control }

func (p *Process) wait(lifetime context.Context, run *execution) {
	err := run.command.Wait()
	if lifetime.Err() != nil {
		// CommandContext reports its private cancellation even after a clean exit.
		if errors.Is(err, context.Canceled) && run.command.ProcessState != nil && run.command.ProcessState.Success() {
			err = nil
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.Sys().(syscall.WaitStatus).Signal() == syscall.SIGTERM {
			err = nil
		}
	}
	run.err = err
	run.cancel()
	p.mu.Lock()
	if p.state == domain.Stopping {
		p.state = domain.Stopped
	} else {
		p.state = domain.Exited
	}
	close(run.done)
	p.mu.Unlock()
}
