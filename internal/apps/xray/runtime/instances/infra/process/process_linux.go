//go:build linux

// Package process owns configured Xray processes and local native API observations.
package process

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type Config struct {
	Binary        string
	Configuration string
	StopTimeout   time.Duration
	API           *APIConfig
	Source        Source
}

type execution struct {
	command    *exec.Cmd
	done       chan struct{}
	input      *Input
	exited     bool
	cleanupErr error
}

// Process owns one execution at a time. A successful Start transfers ownership
// to Process; canceling the start request afterwards does not kill that execution.
type Process struct {
	config   Config
	mutation sync.Mutex
	mu       sync.Mutex
	current  *execution
	snapshot domain.Execution
	closed   bool
	stopping bool
	api      *api
	pending  *Input
}

func New(config Config) (*Process, error) {
	if !filepath.IsAbs(config.Binary) || !filepath.IsAbs(config.Configuration) || config.StopTimeout <= 0 {
		return nil, domain.ErrInvalidArgument
	}
	resource, err := newAPI(config.API)
	if err != nil {
		return nil, err
	}
	return &Process{config: config, snapshot: domain.Execution{State: domain.Stopped}, api: resource}, nil
}

func (p *Process) Observe() domain.Execution {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := p.snapshot
	if result.StartedAt != nil {
		value := *result.StartedAt
		result.StartedAt = &value
	}
	if result.ExitedAt != nil {
		value := *result.ExitedAt
		result.ExitedAt = &value
	}
	if result.ExitCode != nil {
		value := *result.ExitCode
		result.ExitCode = &value
	}
	if result.ConfigurationRevision != nil {
		value := *result.ConfigurationRevision
		result.ConfigurationRevision = &value
	}
	return result
}

func (p *Process) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(domain.ErrNotApplied, err)
	}
	if !p.mutation.TryLock() {
		return errors.Join(domain.ErrNotApplied, domain.ErrBusy)
	}
	defer p.mutation.Unlock()
	if err := p.cleanupPending(ctx); err != nil {
		return errors.Join(domain.ErrNotApplied, err)
	}
	if needed, err := p.needsStart(); err != nil || !needed {
		return err
	}
	command := exec.Command(p.config.Binary, "run", "-c", p.config.Configuration)
	command.Dir = filepath.Dir(p.config.Configuration)
	command.Env = []string{}
	// Nil output descriptors use the OS null device: no pipe readers or retained logs.
	var input *Input
	if p.config.Source != nil {
		var err error
		input, err = p.config.Source.Open(ctx)
		if err == nil {
			err = validateInput(input)
		}
		if err != nil {
			cleanupErr := p.discardInput(input)
			return errors.Join(domain.ErrNotApplied, err, cleanupErr)
		}
		if input != nil {
			command.Args = []string{p.config.Binary, "run"}
			for _, name := range input.Names {
				command.Args = append(command.Args, "-c", "/proc/self/fd/3/"+name)
			}
			command.ExtraFiles = []*os.File{input.Directory}
		}
	}
	if err := ctx.Err(); err != nil {
		cleanupErr := p.discardInput(input)
		return errors.Join(domain.ErrNotApplied, err, cleanupErr)
	}
	err := command.Start()
	if input != nil && input.Directory != nil {
		_ = input.Directory.Close()
	}
	if err != nil {
		err = errors.Join(err, p.discardInput(input))
	}
	if err != nil {
		return errors.Join(domain.ErrNotApplied, err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	started := time.Now().UTC()
	run := &execution{command: command, done: make(chan struct{}), input: input}
	p.current = run
	p.snapshot = domain.Execution{State: domain.Running, Generation: p.snapshot.Generation + 1, StartedAt: &started}
	if input != nil {
		revision := input.Revision
		p.snapshot.ConfigurationRevision = &revision
	}
	go p.wait(run)
	return nil
}

// The mutation lock reserves startup while this short state check holds mu.
// Source staging and process creation never hold the observation lock.
func (p *Process) needsStart() (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case p.closed:
		return false, errors.Join(domain.ErrNotApplied, domain.ErrClosed)
	case p.snapshot.State == domain.Running:
		return false, nil
	case p.current != nil:
		return false, errors.Join(domain.ErrNotApplied, domain.ErrBusy)
	case p.snapshot.Generation == math.MaxUint64:
		return false, errors.Join(domain.ErrNotApplied, errors.New("execution generation exhausted"))
	default:
		return true, nil
	}
}

func (p *Process) wait(run *execution) {
	err := run.command.Wait()
	completed := time.Now().UTC()
	p.mu.Lock()
	p.snapshot.State = domain.Exited
	// Stop marks its intention before signaling. Preserve actual exit evidence.
	if p.current == run && p.stopping {
		p.snapshot.State = domain.Stopped
	}
	p.snapshot.ExitedAt = &completed
	code := int32(run.command.ProcessState.ExitCode())
	p.snapshot.ExitCode = &code
	p.snapshot.Failure = err
	run.exited = true
	p.mu.Unlock()
	cleanupErr := p.cleanupInput(run.input)
	p.mu.Lock()
	defer p.mu.Unlock()
	if cleanupErr != nil {
		p.pending = run.input
		p.snapshot.CleanupFailure = cleanupErr
		run.cleanupErr = errors.Join(domain.ErrCleanupFailed, cleanupErr)
	}
	p.current = nil
	p.stopping = false
	close(run.done)
}

func (p *Process) Stop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(domain.ErrNotApplied, err)
	}
	if !p.mutation.TryLock() {
		return errors.Join(domain.ErrNotApplied, domain.ErrBusy)
	}
	defer p.mutation.Unlock()
	return p.stop(ctx)
}

func (p *Process) stop(ctx context.Context) error {
	p.mu.Lock()
	run := p.current
	if run == nil {
		p.mu.Unlock()
		return p.cleanupPending(ctx)
	}
	if run.exited {
		p.mu.Unlock()
		select {
		case <-run.done:
			return run.cleanupErr
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	p.stopping = true
	p.snapshot.State = domain.Stopping
	p.mu.Unlock()
	signalErr := run.command.Process.Signal(syscall.SIGTERM)
	if errors.Is(signalErr, os.ErrProcessDone) {
		signalErr = nil
	}
	timer := time.NewTimer(p.config.StopTimeout)
	defer timer.Stop()
	select {
	case <-run.done:
		return errors.Join(signalErr, run.cleanupErr)
	case <-ctx.Done():
		p.kill(run)
		// Wait remains owned and observable; canceled callers do not claim a join.
		select {
		case <-run.done:
			return errors.Join(signalErr, run.cleanupErr)
		default:
		}
		return errors.Join(ctx.Err(), signalErr)
	case <-timer.C:
		p.kill(run)
		select {
		case <-run.done:
			return errors.Join(signalErr, run.cleanupErr)
		case <-ctx.Done():
			return errors.Join(ctx.Err(), signalErr)
		}
	}
}

func (p *Process) kill(run *execution) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.current == run && !run.exited {
		p.snapshot.Forced = true
		_ = run.command.Process.Kill()
	}
}

// Close permanently seals admission. It also joins work left by a canceled Stop.
func (p *Process) Close(ctx context.Context) error {
	// The capability's service serializes Close with its mutations. Direct callers
	// must retry an explicit busy failure rather than presume cleanup completed.
	if !p.mutation.TryLock() {
		return domain.ErrBusy
	}
	defer p.mutation.Unlock()
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	err := p.stop(ctx)
	return errors.Join(err, p.api.close())
}

func (p *Process) Check(ctx context.Context) (bool, error) {
	before := p.Observe()
	if before.State != domain.Running {
		return false, nil
	}
	ready, err := p.api.check(ctx)
	after := p.Observe()
	if before.Generation != after.Generation || after.State != domain.Running {
		return false, errors.Join(domain.ErrExecutionChanged, err)
	}
	return ready, err
}

func (p *Process) APIConfigured() bool { return p.config.API != nil }
