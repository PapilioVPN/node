// Package lifecycle owns the state of the configured Xray instance.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type Config struct {
	ID               string
	Executable       string
	ConfigFile       string
	RuntimeDirectory string
}

type child interface {
	socket() string
	wait() error
	terminate() error
	kill() error
}

type execution struct {
	child      child
	done       chan struct{}
	finalState State
	cleanupErr error
}

type Service struct {
	config Config
	launch func(Config) (child, error)
	probe  func(context.Context, string) error

	control  chan struct{}
	shutdown context.Context
	cancel   context.CancelFunc

	mu    sync.RWMutex
	state State
	run   *execution
}

func New(config Config) *Service {
	shutdown, cancel := context.WithCancel(context.Background())
	return &Service{
		config: config, launch: startProcess, probe: waitForReadiness,
		control: make(chan struct{}, 1), shutdown: shutdown, cancel: cancel, state: Stopped,
	}
}

func (service *Service) GetStatus(id string) (Status, error) {
	if service.config.ID == "" || id != service.config.ID {
		return Status{}, ErrNotFound
	}
	service.mu.RLock()
	defer service.mu.RUnlock()
	return Status{ID: id, State: service.state}, nil
}

func (service *Service) Start(ctx context.Context, id string) (Status, error) {
	if err := service.admit(ctx, id); err != nil {
		return Status{}, err
	}
	defer service.release()

	service.mu.Lock()
	switch service.state {
	case Ready:
		service.mu.Unlock()
		return Status{ID: id, State: Ready}, nil
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

	process, err := service.launch(service.config)
	if err != nil {
		service.mu.Lock()
		service.state = Failed
		service.mu.Unlock()
		return Status{}, fmt.Errorf("%w: %w", ErrStartFailed, err)
	}

	run := &execution{child: process, done: make(chan struct{}), finalState: Failed}
	service.mu.Lock()
	service.run = run
	service.mu.Unlock()
	go service.wait(run, cancel)

	probeErr := startCtx.Err()
	if probeErr == nil {
		probeErr = service.probe(startCtx, process.socket())
	}
	service.mu.Lock()
	if probeErr == nil && startCtx.Err() == nil && service.run == run {
		service.state = Ready
		service.mu.Unlock()
		return Status{ID: id, State: Ready}, nil
	}
	if service.run == run {
		service.state = Stopping
	}
	service.mu.Unlock()

	// A request that never reached readiness must not leave an active child.
	killErr := process.kill()
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
	defer cleanupCancel()
	cleanupErr := awaitExit(cleanupCtx, run)
	if err := ctx.Err(); err != nil {
		return Status{}, errors.Join(err, killErr, cleanupErr)
	}
	if service.shutdown.Err() != nil {
		return Status{}, errors.Join(ErrUnavailable, killErr, cleanupErr)
	}
	return Status{}, errors.Join(ErrStartFailed, probeErr, killErr, cleanupErr)
}

func (service *Service) Stop(ctx context.Context, id string) (Status, error) {
	if err := service.admit(ctx, id); err != nil {
		return Status{}, err
	}
	defer service.release()

	if err := service.stop(ctx); err != nil {
		return Status{}, err
	}
	return Status{ID: id, State: Stopped}, nil
}

// Close rejects new mutations and joins any child still owned by the service.
func (service *Service) Close(ctx context.Context) error {
	service.cancel()
	select {
	case service.control <- struct{}{}:
		defer service.release()
		return service.stop(ctx)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (service *Service) admit(ctx context.Context, id string) error {
	if service.config.ID == "" || id != service.config.ID {
		return ErrNotFound
	}
	select {
	case service.control <- struct{}{}:
	default:
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

func (service *Service) release() { <-service.control }

func (service *Service) wait(run *execution, cancelStart context.CancelFunc) {
	run.cleanupErr = run.child.wait()
	service.mu.Lock()
	defer service.mu.Unlock()
	service.run = nil
	service.state = run.finalState
	if run.cleanupErr != nil {
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
	service.mu.Unlock()

	signalErr := run.child.terminate()
	grace := time.NewTimer(500 * time.Millisecond)
	defer grace.Stop()
	select {
	case <-run.done:
		if err := errors.Join(signalErr, run.cleanupErr); err != nil {
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
		return run.cleanupErr
	default:
	}
	select {
	case <-run.done:
		return run.cleanupErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
