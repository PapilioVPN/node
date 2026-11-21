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
}

type execution struct {
	command *exec.Cmd
	done    chan struct{}
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
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.Join(domain.ErrNotApplied, domain.ErrClosed)
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(domain.ErrNotApplied, err)
	}
	if p.snapshot.State == domain.Running {
		return nil
	}
	if p.current != nil {
		return errors.Join(domain.ErrNotApplied, domain.ErrBusy)
	}
	if p.snapshot.Generation == math.MaxUint64 {
		return errors.Join(domain.ErrNotApplied, errors.New("execution generation exhausted"))
	}
	command := exec.Command(p.config.Binary, "run", "-c", p.config.Configuration)
	command.Dir = filepath.Dir(p.config.Configuration)
	command.Env = []string{}
	// Nil output descriptors use the OS null device: no pipe readers or retained logs.
	p.mu.Unlock()
	err := command.Start()
	p.mu.Lock()
	if err != nil {
		return errors.Join(domain.ErrNotApplied, err)
	}
	started := time.Now().UTC()
	run := &execution{command: command, done: make(chan struct{})}
	p.current = run
	p.snapshot = domain.Execution{State: domain.Running, Generation: p.snapshot.Generation + 1, StartedAt: &started}
	go p.wait(run)
	return nil
}

func (p *Process) wait(run *execution) {
	err := run.command.Wait()
	completed := time.Now().UTC()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.snapshot.State = domain.Exited
	// Stop marks its intention before signaling. Preserve actual exit evidence.
	if p.current == run && p.stopping {
		p.snapshot.State = domain.Stopped
	}
	p.snapshot.ExitedAt = &completed
	code := int32(run.command.ProcessState.ExitCode())
	p.snapshot.ExitCode = &code
	p.snapshot.Failure = err
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
		return nil
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
		return signalErr
	case <-ctx.Done():
		p.kill(run)
		// Wait remains owned and observable; canceled callers do not claim a join.
		select {
		case <-run.done:
			return signalErr
		default:
		}
		return errors.Join(ctx.Err(), signalErr)
	case <-timer.C:
		p.kill(run)
		select {
		case <-run.done:
			return signalErr
		case <-ctx.Done():
			return errors.Join(ctx.Err(), signalErr)
		}
	}
}

func (p *Process) kill(run *execution) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.current == run {
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
