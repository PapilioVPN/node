// Package natsserver owns a bounded Core NATS request/reply connection.
package natsserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/platform/tlsconfig"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/natsresponse"
	"github.com/nats-io/nats.go"
)

// Handler receives a server-owned deadline. Reply publication errors are returned.
// Broker mTLS/ACL authorizes the publisher; operation grants belong to its adapter.
type Handler func(context.Context, *nats.Msg) error

type endpoint struct {
	subject string
	handler Handler
}

type Server struct {
	control    sync.Mutex // Lifecycle and registration only, never request I/O.
	gate       sync.Mutex
	stopping   bool
	closed     bool
	active     sync.WaitGroup
	slow       sync.Once
	failure    error
	started    bool
	cfg        settings.NATS
	options    nats.Options
	endpoints  []endpoint
	connection *nats.Conn
	done       chan struct{}
	root       context.Context
	cancel     context.CancelFunc
	onFailure  func()
}

func NewServer(cfg settings.NATS, onFailure func()) (*Server, error) {
	tls, err := tlsconfig.Client(cfg.TLS.CertificateFile, cfg.TLS.PrivateKeyFile, cfg.TLS.CAFile)
	if err != nil {
		return nil, err
	}
	root, cancel := context.WithCancel(context.Background())
	s := &Server{cfg: cfg, root: root, cancel: cancel, done: make(chan struct{}), onFailure: onFailure}
	options := nats.GetDefaultOptions()
	options.Url, options.TLSConfig, options.Secure = cfg.URL, tls, true
	options.AllowReconnect, options.IgnoreDiscoveredServers, options.SkipHostLookup = false, true, true
	options.Timeout, options.FlusherTimeout, options.DrainTimeout = cfg.ConnectTimeout, cfg.ConnectTimeout, cfg.DrainTimeout
	options.AsyncErrorCB = func(_ *nats.Conn, _ *nats.Subscription, err error) {
		if errors.Is(err, nats.ErrSlowConsumer) {
			// Core NATS drops overflowing pending messages; callers can time out.
			// Log once to avoid turning sustained overload into an unbounded log stream.
			s.slow.Do(func() { slog.Warn("NATS pending limit reached; requests may time out") })
			return
		}
		s.recordFailure(err)
	}
	options.ClosedCB = func(conn *nats.Conn) {
		s.gate.Lock()
		stopping := s.stopping
		s.gate.Unlock()
		if !stopping {
			err := conn.LastError()
			if err == nil {
				err = errors.New("NATS connection closed unexpectedly")
			}
			s.recordFailure(err)
		}
		close(s.done)
	}
	s.options = options
	return s, nil
}

// Handle prepares one exact subject before Start. nats.go requires a connected
// client for subscription; the SDK still owns subscription queues and dispatch.
func (s *Server) Handle(suffix string, handler Handler) error {
	s.control.Lock()
	defer s.control.Unlock()
	if s.started || s.closed {
		return errors.New("NATS registration is closed")
	}
	subject := s.cfg.SubjectPrefix + "." + suffix
	for _, existing := range s.endpoints {
		if existing.subject == subject {
			return errors.New("duplicate NATS endpoint")
		}
	}
	s.endpoints = append(s.endpoints, endpoint{subject, handler})
	return nil
}

func (s *Server) Start(ctx context.Context) error {
	s.control.Lock()
	defer s.control.Unlock()
	if s.started || s.closed {
		return errors.New("NATS server is closed or already started")
	}
	s.started = true
	startup, cancel := context.WithTimeout(ctx, s.cfg.ConnectTimeout)
	defer cancel()
	dialer := &startupDialer{ctx: startup}
	s.options.CustomDialer = dialer
	conn, err := s.options.Connect()
	dialer.release()
	if err != nil {
		return fmt.Errorf("connect NATS: %w", errors.Join(startup.Err(), err))
	}
	s.connection = conn
	for _, endpoint := range s.endpoints {
		handler := endpoint.handler
		subscription, err := conn.QueueSubscribe(endpoint.subject, s.cfg.QueueGroup, func(message *nats.Msg) { s.handle(message, handler) })
		if err == nil {
			err = subscription.SetPendingLimits(s.cfg.MaxPendingMessages, s.cfg.MaxPendingBytes)
		}
		if err != nil {
			s.close()
			return fmt.Errorf("subscribe NATS: %w", err)
		}
	}
	if err := conn.FlushWithContext(startup); err != nil {
		s.close()
		return fmt.Errorf("flush NATS subscriptions: %w", err)
	}
	if err := conn.LastError(); err != nil {
		s.close()
		return fmt.Errorf("NATS subscription rejected: %w", err)
	}
	s.gate.Lock()
	failure := s.failure
	s.gate.Unlock()
	if failure != nil {
		s.close()
		return failure
	}
	return nil
}

func (s *Server) handle(message *nats.Msg, handler Handler) {
	if message.Reply == "" {
		return
	} // Request/reply only; never execute fire-and-forget effects.
	s.gate.Lock()
	if s.closed {
		s.gate.Unlock()
		return
	}
	s.active.Add(1)
	s.gate.Unlock()
	defer s.active.Done()
	if len(message.Data) > s.cfg.MaxRequestBytes {
		if err := natsresponse.Error(message, "resource_exhausted", "request exceeds configured byte limit"); err != nil {
			s.recordFailure(err)
		}
		return
	}
	ctx, cancel := context.WithTimeout(s.root, s.cfg.RequestTimeout)
	defer cancel()
	if deadlines := message.Header.Values("Papilio-Deadline"); len(deadlines) > 0 {
		deadline, err := time.Parse(time.RFC3339Nano, deadlines[0])
		if err != nil || len(deadlines) != 1 {
			if err := natsresponse.Error(message, "invalid_argument", "invalid Papilio-Deadline header"); err != nil {
				s.recordFailure(err)
			}
			return
		}
		child, release := context.WithDeadline(ctx, deadline)
		defer release()
		ctx = child
	}
	if ctx.Err() != nil {
		if err := natsresponse.Error(message, "deadline_exceeded", "request deadline exceeded"); err != nil {
			s.recordFailure(err)
		}
		return
	}
	if err := handler(ctx, message); err != nil {
		s.recordFailure(err)
	}
}

// Stop drains native subscriptions/replies within ctx, then forces cancellation.
func (s *Server) Stop(ctx context.Context) error {
	s.control.Lock()
	defer s.control.Unlock()
	if s.closed {
		return nil
	}
	var stopErr error
	s.gate.Lock()
	s.stopping = true
	s.gate.Unlock()
	if s.connection != nil {
		if err := s.connection.Drain(); err != nil && !errors.Is(err, nats.ErrConnectionClosed) {
			s.recordFailure(err)
		}
		select {
		case <-s.done:
		case <-ctx.Done():
			stopErr = ctx.Err()
		}
	}
	s.close()
	s.gate.Lock()
	defer s.gate.Unlock()
	return errors.Join(s.failure, stopErr)
}

func (s *Server) Close() error {
	s.control.Lock()
	defer s.control.Unlock()
	if s.closed {
		return nil
	}
	s.close()
	s.gate.Lock()
	defer s.gate.Unlock()
	return s.failure
}

func (s *Server) close() {
	s.gate.Lock()
	s.stopping, s.closed = true, true
	s.gate.Unlock()
	s.cancel()
	if s.connection != nil {
		s.connection.Close()
		<-s.done
	}
	// Admission is closed under the same lock as Add, so no late callback can
	// race Wait or continue using modules after final cleanup.
	s.active.Wait()
}

func (s *Server) recordFailure(err error) {
	s.gate.Lock()
	notify := s.failure == nil && !s.stopping
	if !s.closed && s.failure == nil {
		s.failure = err
	}
	s.gate.Unlock()
	if notify && s.onFailure != nil {
		s.onFailure()
	}
}
