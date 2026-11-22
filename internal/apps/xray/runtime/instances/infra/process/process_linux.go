//go:build linux

// Package process owns configured Xray processes and local native API observations.
package process

import (
	"context"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"golang.org/x/sys/unix"
)

type Config struct {
	ID                string
	RecoveryDirectory string
	WorkDirectory     string
	Binary            string
	Configuration     string
	StopTimeout       time.Duration
	API               *rpc.Client
	Source            Source
}

type execution struct {
	pidfd       int
	pid         int
	record      executionRecord
	output      *os.File
	finalOutput uint64
	position    atomic.Uint64
	adopted     bool
	cancel      context.CancelFunc
	watchDone   chan struct{}
	command     *exec.Cmd
	done        chan struct{}
	input       *Input
	exited      bool
	cleanupErr  error
}

// Process owns one execution at a time. A successful Start transfers ownership
// to Process; canceling the start request afterwards does not kill that execution.
type Process struct {
	records    *recordStore
	lastOutput *execution
	retain     bool
	config     Config
	mutation   sync.Mutex
	mu         sync.Mutex
	current    *execution
	snapshot   domain.Execution
	closed     bool
	stopping   bool
	api        *api
	pending    *Input
}

func New(config Config) (*Process, error) {
	if !filepath.IsAbs(config.Binary) || !filepath.IsAbs(config.Configuration) || config.StopTimeout <= 0 {
		return nil, domain.ErrInvalidArgument
	}
	if config.RecoveryDirectory != "" && (config.ID == "" || !filepath.IsAbs(config.RecoveryDirectory) || (config.WorkDirectory != "" && !filepath.IsAbs(config.WorkDirectory))) {
		return nil, domain.ErrInvalidArgument
	}
	resource := newAPI(config.API)
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

func (p *Process) Start(ctx context.Context) (startErr error) {
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
	p.mu.Lock()
	busy := p.current != nil && p.snapshot.State != domain.Running
	p.mu.Unlock()
	if busy {
		return errors.Join(domain.ErrNotApplied, domain.ErrBusy)
	}
	if p.Observe().State != domain.Running {
		if adopted, err := p.restore(ctx); err != nil || adopted {
			return err
		}
	}
	if needed, err := p.needsStart(); err != nil || !needed {
		return err
	}
	command := exec.Command(p.config.Binary, "run", "-c", p.config.Configuration)
	command.Dir = filepath.Dir(p.config.Configuration)
	command.Env = []string{}
	output, err := newOutput()
	if err != nil {
		return errors.Join(domain.ErrNotApplied, err)
	}
	defer func() {
		if output != nil {
			output.Close()
		}
	}()
	command.Stdout = output
	command.Stderr = output
	token, err := randomID()
	if err != nil {
		return errors.Join(domain.ErrNotApplied, err)
	}
	executionID, err := randomID()
	if err != nil {
		return errors.Join(domain.ErrNotApplied, err)
	}
	command.Env = []string{tokenEnvironment + "=" + token}
	pidfd := -1
	command.SysProcAttr = &syscall.SysProcAttr{PidFD: &pidfd}
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
	ownedInput := input
	defer func() {
		if ownedInput != nil {
			startErr = errors.Join(startErr, p.discardInput(ownedInput))
		}
	}()
	if err := ctx.Err(); err != nil {
		return errors.Join(domain.ErrNotApplied, err)
	}
	record := executionRecord{Version: 1, InstanceID: p.config.ID, Token: token, ExecutionID: executionID, Generation: p.Observe().Generation + 1, StartedAt: time.Now().UTC(), Pending: true}
	record.BootID, err = bootIdentity()
	if err != nil {
		return errors.Join(domain.ErrNotApplied, err)
	}
	record.BinaryDevice, record.BinaryInode, err = statIdentity(p.config.Binary)
	if err != nil {
		return errors.Join(domain.ErrNotApplied, err)
	}
	var outstat unix.Stat_t
	if err := unix.Fstat(int(output.Fd()), &outstat); err != nil {
		return errors.Join(domain.ErrNotApplied, err)
	}
	record.OutputDevice = uint64(outstat.Dev)
	record.OutputInode = outstat.Ino
	if input != nil {
		revision := input.Revision
		record.Revision = &revision
		record.SourceDirectory = input.Directory.Name()
		record.SourceDevice, record.SourceInode, err = statIdentity(record.SourceDirectory)
		if err != nil {
			return errors.Join(domain.ErrNotApplied, err)
		}
	}
	if p.records != nil {
		if err := p.records.write(record); err != nil {
			return errors.Join(domain.ErrNotApplied, err)
		}
	}
	err = command.Start()
	if input != nil && input.Directory != nil {
		_ = input.Directory.Close()
	}
	if err != nil {
		if p.records != nil {
			record.Pending = false
			record.Generation--
			err = errors.Join(err, p.records.write(record))
		}
		return errors.Join(domain.ErrNotApplied, err)
	}
	record.PID = command.Process.Pid
	record.Pending = false
	record.StartTicks, _, err = processIdentity(record.PID)
	if err != nil {
		command.Process.Kill()
		command.Wait()
		if pidfd >= 0 {
			unix.Close(pidfd)
		}
		if p.records != nil {
			record.PID = 0
			record.Pending = false
			err = errors.Join(err, p.records.write(record))
		}
		return errors.Join(domain.ErrCleanupFailed, err)
	}
	if pidfd < 0 {
		pidfd, err = unix.PidfdOpen(record.PID, 0)
	}
	if err != nil {
		command.Process.Kill()
		command.Wait()
		return err
	}
	if p.records != nil {
		if err := p.records.write(record); err != nil {
			command.Process.Kill()
			command.Wait()
			unix.Close(pidfd)
			return errors.Join(domain.ErrCleanupFailed, err)
		}
	}
	watch, cancel := context.WithCancel(context.Background())
	run := &execution{command: command, pidfd: pidfd, pid: record.PID, record: record, output: output, done: make(chan struct{}), watchDone: make(chan struct{}), cancel: cancel, input: input}
	output = nil
	p.mu.Lock()
	old := p.lastOutput
	p.lastOutput = run
	p.current = run
	p.retain = false
	p.snapshot = domain.Execution{State: domain.Running, Generation: record.Generation, StartedAt: &record.StartedAt, ConfigurationRevision: record.Revision, ExecutionID: executionID, IdentityVerified: true}
	p.mu.Unlock()
	if old != nil && old.output != nil {
		old.output.Close()
	}
	ownedInput = nil
	go p.watch(watch, run)
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

func (p *Process) watch(ctx context.Context, run *execution) {
	defer close(run.watchDone)
	defer func() { p.mu.Lock(); defer p.mu.Unlock(); unix.Close(run.pidfd); run.pidfd = -1 }()
	for {
		if ctx.Err() != nil {
			return
		}
		if run.adopted {
			if pos, err := outputPosition(run.pid); err == nil {
				run.position.Store(min(pos, domain.MaxOutputBytes))
			}
		}
		fds := []unix.PollFd{{Fd: int32(run.pidfd), Events: unix.POLLIN}}
		_, err := unix.Poll(fds, 25)
		if err == unix.EINTR {
			continue
		}
		if err != nil || fds[0].Revents&(unix.POLLIN|unix.POLLHUP) != 0 {
			break
		}
	}
	var err error
	var exit *int32
	if run.command != nil {
		err = run.command.Wait()
		code := int32(run.command.ProcessState.ExitCode())
		exit = &code
	}
	if run.adopted {
		var info unix.Siginfo
		_ = unix.Waitid(unix.P_PIDFD, run.pidfd, &info, unix.WEXITED|unix.WNOHANG, nil)
		err = errors.New("reattached execution exited without wait status")
	}
	position, _ := run.output.Seek(0, io.SeekCurrent)
	if run.adopted {
		position = int64(run.position.Load())
	}
	cleanupErr := p.cleanupInput(run.input)
	if p.records != nil {
		record := run.record
		record.PID = 0
		record.Pending = false
		if cleanupErr == nil {
			record.SourceDirectory = ""
		}
		cleanupErr = errors.Join(cleanupErr, p.records.write(record))
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	run.finalOutput = uint64(max(0, position))
	p.snapshot.State = domain.Exited
	if p.stopping {
		p.snapshot.State = domain.Stopped
	}
	completed := time.Now().UTC()
	p.snapshot.ExitedAt = &completed
	p.snapshot.ExitCode = exit
	p.snapshot.Failure = err
	run.exited = true
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
		last := p.lastOutput
		p.mu.Unlock()
		if last != nil {
			if err := joinWatcher(ctx, last); err != nil {
				return err
			}
		}
		return p.cleanupPending(ctx)
	}
	if run.exited {
		p.mu.Unlock()
		select {
		case <-run.done:
			return errors.Join(run.cleanupErr, joinWatcher(ctx, run))
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	p.stopping = true
	p.snapshot.State = domain.Stopping
	signalErr := unix.PidfdSendSignal(run.pidfd, unix.SIGTERM, nil, 0)
	p.mu.Unlock()
	if errors.Is(signalErr, os.ErrProcessDone) || errors.Is(signalErr, unix.ESRCH) {
		signalErr = nil
	}
	timer := time.NewTimer(p.config.StopTimeout)
	defer timer.Stop()
	select {
	case <-run.done:
		return errors.Join(signalErr, run.cleanupErr, joinWatcher(ctx, run))
	case <-ctx.Done():
		p.kill(run)
		// Wait remains owned and observable; canceled callers do not claim a join.
		select {
		case <-run.done:
			return errors.Join(signalErr, run.cleanupErr, joinWatcher(ctx, run))
		default:
		}
		return errors.Join(ctx.Err(), signalErr)
	case <-timer.C:
		p.kill(run)
		select {
		case <-run.done:
			return errors.Join(signalErr, run.cleanupErr, joinWatcher(ctx, run))
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
		_ = unix.PidfdSendSignal(run.pidfd, unix.SIGKILL, nil, 0)
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
	if p.retain {
		return p.release(ctx)
	}
	err := p.stop(ctx)
	p.mu.Lock()
	output := p.lastOutput
	p.mu.Unlock()
	if output != nil && output.output != nil {
		err = errors.Join(err, closeOutput(output.output))

	}
	p.mu.Lock()
	pending := p.current != nil
	p.mu.Unlock()
	if pending {
		return errors.Join(err, p.api.close())
	}
	return errors.Join(err, p.api.close(), p.records.close())
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

func joinWatcher(ctx context.Context, run *execution) error {
	select {
	case <-run.watchDone:
		return nil
	default:
	}
	select {
	case <-run.watchDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
