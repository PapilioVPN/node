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
	executableMatches(string) bool
	wait() processExit
	terminate() error
	kill() error
}

type execution struct {
	child            child
	logs             *live.Execution
	done             chan struct{}
	finalState       State
	exit             processExit
	exited           bool
	conn             *grpc.ClientConn
	desired          desiredConfig
	keepAlive        time.Duration
	expiresAt        time.Time
	routingWrites    *semaphore.Weighted
	routingUncertain bool // protected by routingWrites
}

type Service struct {
	config             Config
	initializeAccounts AccountInitializer
	logs               *live.Service
	launch             func(context.Context, Config, []byte, *live.Execution) (child, error)
	probe              func(context.Context, grpc.ClientConnInterface) error
	health             func(context.Context, grpc.ClientConnInterface) error

	control      *semaphore.Weighted
	shutdown     context.Context
	cancel       context.CancelFunc
	leaseChanged chan struct{}
	leaseDone    chan struct{} // written under control

	mu    sync.RWMutex
	state State
	run   *execution
}

func New(config Config, logs *live.Service) *Service {
	shutdown, cancel := context.WithCancel(context.Background())
	return &Service{
		config: config, logs: logs, launch: startProcess, probe: waitForReadiness, health: waitForReadiness,
		control: semaphore.NewWeighted(1), shutdown: shutdown, cancel: cancel, state: Stopped,
		leaseChanged: make(chan struct{}, 1),
	}
}

// SetAccountInitializer registers the accounts capability during composition.
func (service *Service) SetAccountInitializer(initializer AccountInitializer) {
	service.initializeAccounts = initializer
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
		if service.state == Ready {
			status.LeaseExpiresAt = service.run.expiresAt
		}
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

func (service *Service) launchReady(ctx context.Context, desired desiredConfig, users []InboundUsers, keepAlive time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	service.mu.Lock()
	service.state = Starting
	service.mu.Unlock()

	startCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	logs := service.logs.Prepare()
	process, err := service.launch(startCtx, service.config, desired.source, logs)
	if err != nil {
		logs.End()
		service.mu.Lock()
		service.state = Failed
		service.mu.Unlock()
		if ctx.Err() != nil {
			return errors.Join(ctx.Err(), err)
		}
		return &StartError{Reason: LaunchFailed, cause: err}
	}

	run := &execution{child: process, logs: logs, done: make(chan struct{}), finalState: Failed, desired: desired,
		routingWrites: semaphore.NewWeighted(1)}
	service.mu.Lock()
	service.run = run
	service.mu.Unlock()
	go service.wait(run, cancel)

	var conn *grpc.ClientConn
	probeErr := startCtx.Err()
	if probeErr == nil {
		conn, probeErr = openNativeConnection(process.socket(), run.serializeRoutingWrites)
		if probeErr == nil {
			probeErr = service.probe(startCtx, conn)
		}
	}
	var commitAccounts func()
	var seedErr error
	if probeErr == nil && service.initializeAccounts != nil {
		commitAccounts, seedErr = service.initializeAccounts(startCtx, service.config.ID, conn, users, desired.excluded)
	} else if probeErr == nil && len(users) > 0 {
		seedErr = ErrUnavailable
	}
	service.mu.Lock()
	startErr := startCtx.Err()
	if probeErr == nil && seedErr == nil && startErr == nil && !run.exited {
		if commitAccounts != nil {
			commitAccounts()
		}
		run.conn = conn
		service.state = Ready
		logs.Activate()
		service.refreshLeaseLocked(run, keepAlive)
		service.mu.Unlock()
		return nil
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
		if seedErr != nil {
			failure.Reason = InitialAccountsFailed
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
		return errors.Join(err, killErr, cleanupErr)
	}
	failure.cause = errors.Join(exitErr, startErr, probeErr, seedErr, killErr, cleanupErr)
	return &failure
}

func (service *Service) Stop(ctx context.Context, id string) (Status, error) {
	if err := service.admit(ctx, id); err != nil {
		return Status{}, err
	}
	defer service.release()

	err := service.stop(ctx, Stopped)
	status, _ := service.GetStatus(id)
	return status, err
}

// Close rejects new mutations and joins any child still owned by the service.
func (service *Service) Close(ctx context.Context) error {
	service.cancel()
	if err := service.control.Acquire(ctx, 1); err != nil {
		return err
	}
	leaseDone := service.leaseDone
	err := service.stop(ctx, Stopped)
	service.release()
	if leaseDone != nil {
		select {
		case <-leaseDone:
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		}
	}
	return err
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
	service.wakeLease()
	service.run = nil
	service.state = run.finalState
	if run.exit.cleanupErr != nil {
		service.state = Failed
	}
	close(run.done)
	cancelStart()
}

func (service *Service) stop(ctx context.Context, finalState State) error {
	service.mu.Lock()
	run := service.run
	if run == nil {
		service.state = finalState
		service.mu.Unlock()
		return nil
	}
	service.state = Stopping
	run.finalState = finalState
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
