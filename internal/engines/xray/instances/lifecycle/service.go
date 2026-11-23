// Package lifecycle owns the state of the configured Xray instance.
package lifecycle

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/buildinfo"
	"github.com/PapilioVPN/node/internal/engines/xray/logs/live"
	"golang.org/x/sync/semaphore"
	"google.golang.org/grpc"
)

type Config struct {
	ID               string
	Executable       string
	ConfigFile       string
	RuntimeDirectory string
}

type child interface {
	socket() string
	version() string
	wait() processExit
	terminate() error
	kill() error
}

type execution struct {
	child      child
	logs       *live.Execution
	done       chan struct{}
	finalState State
	exit       processExit
	exited     bool
	conn       *grpc.ClientConn
}

type Service struct {
	config Config
	logs   *live.Service
	launch func(context.Context, Config, *live.Execution) (child, error)
	probe  func(context.Context, grpc.ClientConnInterface) error

	control  *semaphore.Weighted
	shutdown context.Context
	cancel   context.CancelFunc

	mu    sync.RWMutex
	state State
	run   *execution
}

func New(config Config, logs *live.Service) *Service {
	shutdown, cancel := context.WithCancel(context.Background())
	return &Service{
		config: config, logs: logs, launch: startProcess, probe: waitForReadiness,
		control: semaphore.NewWeighted(1), shutdown: shutdown, cancel: cancel, state: Stopped,
	}
}

func (service *Service) GetStatus(id string) (Status, error) {
	if service.config.ID == "" || id != service.config.ID {
		return Status{}, ErrNotFound
	}
	service.mu.RLock()
	defer service.mu.RUnlock()
	return service.statusLocked(), nil
}

func (service *Service) statusLocked() Status {
	status := Status{ID: service.config.ID, State: service.state, NodeVersion: buildinfo.Version}
	if service.run != nil {
		status.XrayVersion = service.run.child.version()
	}
	return status
}

// NativeConnection returns the current execution's connection. The lifecycle
// owner closes it after the process exits.
func (service *Service) NativeConnection(id string) (*grpc.ClientConn, error) {
	if service.config.ID == "" || id != service.config.ID {
		return nil, ErrNotFound
	}
	service.mu.RLock()
	defer service.mu.RUnlock()
	if service.state != Ready {
		return nil, ErrNotReady
	}
	return service.run.conn, nil
}

func (service *Service) Start(ctx context.Context, id string) (Status, error) {
	if err := service.admit(ctx, id); err != nil {
		return Status{}, err
	}
	defer service.release()

	service.mu.Lock()
	switch service.state {
	case Ready:
		status := service.statusLocked()
		service.mu.Unlock()
		return status, nil
	case Starting, Stopping:
		service.mu.Unlock()
		return Status{}, ErrBusy
	}
	service.state = Starting
	service.mu.Unlock()

	startCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	stop := context.AfterFunc(service.shutdown, cancel)
	defer stop()
	if service.shutdown.Err() != nil {
		cancel()
	}

	logs := service.logs.Prepare()
	process, err := service.launch(startCtx, service.config, logs)
	if err != nil {
		logs.End()
		service.mu.Lock()
		service.state = Failed
		service.mu.Unlock()
		return Status{}, &StartError{Reason: LaunchFailed, cause: err}
	}

	run := &execution{child: process, logs: logs, done: make(chan struct{}), finalState: Failed}
	service.mu.Lock()
	service.run = run
	service.mu.Unlock()
	go service.wait(run, cancel)

	var conn *grpc.ClientConn
	probeErr := startCtx.Err()
	if probeErr == nil {
		conn, probeErr = openNativeConnection(process.socket())
		if probeErr == nil {
			probeErr = service.probe(startCtx, conn)
		}
	}
	service.mu.Lock()
	startErr := startCtx.Err()
	if probeErr == nil && startErr == nil && !run.exited {
		run.conn = conn
		service.state = Ready
		logs.Activate()
		status := service.statusLocked()
		service.mu.Unlock()
		return status, nil
	}
	var failure StartError
	var exitErr error
	exited := run.exited
	if exited {
		failure.Reason = ExitedBeforeReady
		exitErr = run.exit.err
		if run.exit.code >= 0 {
			code := int32(run.exit.code)
			failure.ExitCode = &code
		}
	} else {
		service.state = Stopping
		failure.Reason = ReadinessFailed
		if errors.Is(startErr, context.DeadlineExceeded) || errors.Is(probeErr, context.DeadlineExceeded) {
			failure.Reason = ReadinessTimeout
		}
	}
	service.mu.Unlock()

	// A request that never reached readiness must not leave an active child.
	var killErr error
	if !exited {
		killErr = process.kill()
	}
	if conn != nil {
		killErr = errors.Join(killErr, conn.Close())
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
	defer cleanupCancel()
	cleanupErr := awaitExit(cleanupCtx, run)
	if err := ctx.Err(); err != nil {
		return Status{}, errors.Join(err, killErr, cleanupErr)
	}
	if service.shutdown.Err() != nil {
		return Status{}, errors.Join(ErrUnavailable, killErr, cleanupErr)
	}
	failure.cause = errors.Join(exitErr, startErr, probeErr, killErr, cleanupErr)
	return Status{}, &failure
}

func (service *Service) Stop(ctx context.Context, id string) (Status, error) {
	if err := service.admit(ctx, id); err != nil {
		return Status{}, err
	}
	defer service.release()

	if err := service.stop(ctx); err != nil {
		return Status{}, err
	}
	return service.GetStatus(id)
}

// Close rejects new mutations and joins any child still owned by the service.
func (service *Service) Close(ctx context.Context) error {
	service.cancel()
	if err := service.control.Acquire(ctx, 1); err != nil {
		return err
	}
	defer service.release()
	return service.stop(ctx)
}

func (service *Service) admit(ctx context.Context, id string) error {
	if service.config.ID == "" || id != service.config.ID {
		return ErrNotFound
	}
	if !service.control.TryAcquire(1) {
		return ErrBusy
	}
	if service.shutdown.Err() != nil {
		service.release()
		return ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		service.release()
		return err
	}
	return nil
}

func (service *Service) release() { service.control.Release(1) }

func (service *Service) wait(run *execution, cancelStart context.CancelFunc) {
	exit := run.child.wait()
	service.mu.Lock()
	run.logs.End()
	defer service.mu.Unlock()
	run.exit = exit
	run.exited = true
	if run.conn != nil {
		service.state = Stopping
		service.mu.Unlock()
		closeErr := run.conn.Close()
		service.mu.Lock()
		run.exit.cleanupErr = errors.Join(exit.cleanupErr, closeErr)
	}
	service.run = nil
	service.state = run.finalState
	if run.exit.cleanupErr != nil {
		service.state = Failed
	}
	close(run.done)
	cancelStart()
}

func (service *Service) stop(ctx context.Context) error {
	service.mu.Lock()
	run := service.run
	if run == nil {
		service.state = Stopped
		service.mu.Unlock()
		return nil
	}
	service.state = Stopping
	run.finalState = Stopped
	run.logs.End()
	service.mu.Unlock()

	signalErr := run.child.terminate()
	grace := time.NewTimer(500 * time.Millisecond)
	defer grace.Stop()
	select {
	case <-run.done:
		if err := errors.Join(signalErr, run.exit.cleanupErr); err != nil {
			return errors.Join(ErrStopFailed, err)
		}
		return nil
	case <-grace.C:
	case <-ctx.Done():
	}
	killErr := run.child.kill()
	if err := errors.Join(killErr, awaitExit(ctx, run)); err != nil {
		return errors.Join(ErrStopFailed, err)
	}
	return nil
}

func awaitExit(ctx context.Context, run *execution) error {
	select {
	case <-run.done:
		return run.exit.cleanupErr
	default:
	}
	select {
	case <-run.done:
		return run.exit.cleanupErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
