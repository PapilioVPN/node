// Package lifecycle owns the state of the configured OpenVPN instance.
package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/PapilioVPN/node/internal/buildinfo"
	"golang.org/x/sync/semaphore"
)

type Service struct {
	id          string
	configFile  string
	driver      Driver
	revocations Revocations
	control     *semaphore.Weighted
	logReaders  chan struct{}
	shutdown    context.Context
	cancel      context.CancelFunc

	mu     sync.RWMutex
	status Status
	run    Execution
}

func New(id, configFile string, driver Driver, revocations Revocations) *Service {
	shutdown, cancel := context.WithCancel(context.Background())
	return &Service{
		id: id, configFile: configFile, driver: driver, revocations: revocations,
		control: semaphore.NewWeighted(1), logReaders: make(chan struct{}, 8), shutdown: shutdown, cancel: cancel,
		status: Status{ID: id, State: Stopped, NodeVersion: buildinfo.Version},
	}
}

func (s *Service) GetStatus(id string) (Status, error) {
	if !s.matches(id) {
		return Status{}, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status, nil
}

func (s *Service) Validate(ctx context.Context, id string, proposed []byte) (Validation, error) {
	ctx, done, err := s.begin(ctx, id, 4*time.Second)
	if err != nil {
		return Validation{}, err
	}
	defer done()
	source, err := s.source(proposed)
	if err != nil {
		return Validation{}, err
	}
	issue, err := s.driver.Check(ctx, source)
	return Validation{ID: id, Issue: issue}, err
}

func (s *Service) Start(ctx context.Context, id string, proposed []byte) (Status, error) {
	ctx, done, err := s.begin(ctx, id, 8*time.Second)
	if err != nil {
		return s.result(id, "", err)
	}
	defer done()
	source, err := s.source(proposed)
	if err != nil {
		return s.result(id, "", err)
	}
	hash := sha256.Sum256(source)
	digest := hex.EncodeToString(hash[:])
	s.mu.RLock()
	run, state, applied := s.run, s.status.State, s.status.AppliedConfigSHA256
	s.mu.RUnlock()

	if state == Ready && applied == digest && running(run) {
		return s.result(id, Heartbeat, nil)
	}

	issue, err := s.driver.Check(ctx, source)
	if err != nil {
		return s.result(id, "", err)
	}
	if issue.Code != "" {
		return s.result(id, "", ErrRejected)
	}

	action := Started
	if running(run) {
		if err := ctx.Err(); err != nil {
			return s.result(id, "", err)
		}
		if err := s.stop(ctx); err != nil {
			return s.result(id, "", err)
		}
		action = Restarted
	} else if state == Failed || run != nil {
		action = Recovered
	}
	s.mu.Lock()
	s.run = nil
	s.status.State = Starting
	s.status.AppliedConfigSHA256 = ""
	s.status.OpenVPNVersion = ""
	s.status.ExecutionID = ""
	s.mu.Unlock()
	run, err = s.driver.Start(ctx, source)
	if err != nil {
		s.mu.Lock()
		s.status.State = Failed
		s.mu.Unlock()
		return s.result(id, action, err)
	}
	s.mu.Lock()
	s.run = run
	s.status.State = Ready
	s.status.AppliedConfigSHA256 = digest
	s.status.OpenVPNVersion = run.Version()
	s.status.ExecutionID = run.ExecutionID()
	s.mu.Unlock()
	go s.observe(run)
	result, err := s.result(id, action, nil)
	if !running(run) {
		s.exited(run)
		return s.result(id, action, ErrStartFailed)
	}
	return result, err
}

func (s *Service) Stop(ctx context.Context, id string) (Status, error) {
	ctx, done, err := s.begin(ctx, id, 4*time.Second)
	if err != nil {
		return s.result(id, "", err)
	}
	defer done()
	return s.result(id, "", s.stop(ctx))
}

func (s *Service) Close(ctx context.Context) error {
	s.cancel()
	if err := s.control.Acquire(ctx, 1); err != nil {
		return err
	}
	defer s.control.Release(1)
	return s.stop(ctx)
}

func (s *Service) matches(id string) bool { return id != "" && s.id == id }

func (s *Service) begin(ctx context.Context, id string, timeout time.Duration) (context.Context, func(), error) {
	if !s.matches(id) {
		return nil, nil, ErrNotFound
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if !s.control.TryAcquire(1) {
		return nil, nil, ErrBusy
	}
	if s.shutdown.Err() != nil {
		s.control.Release(1)
		return nil, nil, context.Canceled
	}
	work, cancel := context.WithTimeout(ctx, timeout)
	unlink := context.AfterFunc(s.shutdown, cancel)
	return work, func() {
		unlink()
		cancel()
		s.control.Release(1)
	}, nil
}

func (s *Service) source(proposed []byte) ([]byte, error) {
	if proposed == nil {
		file, err := os.OpenFile(s.configFile, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, errors.Join(ErrSourceUnavailable, err)
		}
		defer file.Close()
		stat, err := file.Stat()
		if err != nil || !stat.Mode().IsRegular() {
			return nil, errors.Join(ErrSourceUnavailable, err)
		}
		proposed, err = io.ReadAll(io.LimitReader(file, MaxSourceBytes+1))
		if err != nil {
			return nil, errors.Join(ErrSourceUnavailable, err)
		}
	}
	if len(proposed) > MaxSourceBytes {
		return nil, ErrTooLarge
	}
	return proposed, nil
}

func (s *Service) result(id string, action StartAction, err error) (Status, error) {
	result, _ := s.GetStatus(id)
	result.StartAction = action
	return result, err
}

func (s *Service) stop(ctx context.Context) error {
	s.mu.Lock()
	run := s.run
	s.status.AppliedConfigSHA256 = ""
	s.status.OpenVPNVersion = ""
	s.status.ExecutionID = ""
	if run == nil {
		s.status.State = Stopped
		s.mu.Unlock()
		return nil
	}
	s.status.State = Stopping
	s.mu.Unlock()

	if err := run.Stop(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	s.run = nil
	s.status.State = Stopped
	s.mu.Unlock()
	return nil
}

func (s *Service) observe(run Execution) {
	<-run.Done()
	s.exited(run)
}

func (s *Service) exited(run Execution) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run != run {
		return
	}
	if s.status.State == Stopping {
		s.status.State = Stopped
	} else {
		s.status.State = Failed
	}
	s.status.AppliedConfigSHA256 = ""
	s.status.OpenVPNVersion = ""
	s.status.ExecutionID = ""
	s.run = nil
}

func running(run Execution) bool {
	if run == nil {
		return false
	}
	select {
	case <-run.Done():
		return false
	default:
		return true
	}
}
