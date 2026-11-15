// Package httpserver owns the Fiber listener, connection bounds and request lifetime.
package httpserver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/valyala/fasthttp"

	"github.com/PapilioVPN/node/internal/platform/tlsconfig"
	"github.com/PapilioVPN/node/internal/settings"
)

// Server starts once. Only lifecycle mutations share the control lock.
type Server struct {
	control     sync.Mutex
	connections sync.Mutex
	active      sync.WaitGroup
	peers       map[net.Conn]struct{}
	app         *fiber.App
	tls         *tls.Config
	address     string
	cancel      context.CancelFunc
	closed      bool
	listener    net.Listener
	done        chan struct{}
	serveErr    error // Read after done closes.
	onFailure   func()
}

func NewServer(cfg settings.HTTP, onFailure func()) (*Server, error) {
	tls, err := tlsconfig.Server(cfg.TLS.CertificateFile, cfg.TLS.PrivateKeyFile, cfg.TLS.ClientCAFile)
	if err != nil {
		return nil, err
	}
	root, cancel := context.WithCancel(context.Background())
	s := &Server{tls: tls, address: cfg.Listen, cancel: cancel, onFailure: onFailure, peers: make(map[net.Conn]struct{})}
	s.app = fiber.New(fiber.Config{
		Concurrency: cfg.MaxConnections, BodyLimit: cfg.MaxBodyBytes, ReadBufferSize: cfg.MaxHeaderBytes,
		ReadTimeout: cfg.ReadTimeout, WriteTimeout: cfg.WriteTimeout, IdleTimeout: cfg.IdleTimeout,
		ErrorHandler: func(c fiber.Ctx, err error) error {
			var native *fiber.Error
			if errors.As(err, &native) {
				return c.Status(native.Code).SendString(native.Message)
			}
			return c.Status(fiber.StatusInternalServerError).SendString("internal server error")
		},
	})
	// Native connection state is needed because fasthttp's deadline stops waiting
	// without closing active sockets. It is not a second request/worker registry.
	s.app.Server().ConnState = s.connectionState
	capacity := make(chan struct{}, cfg.MaxConcurrent)
	s.app.Use(recover.New())
	s.app.Use(func(c fiber.Ctx) error {
		select {
		case capacity <- struct{}{}:
			defer func() { <-capacity }()
		default:
			return fiber.ErrTooManyRequests
		}
		// Fiber's pooled Ctx is not a cancelable request context. Services receive
		// this explicit deadline and process cancellation; disconnects alone do not cancel.
		ctx, cancel := context.WithTimeout(root, cfg.RequestTimeout)
		defer cancel()
		c.SetContext(ctx)
		if err := ctx.Err(); err != nil {
			return fiber.ErrServiceUnavailable
		}
		return c.Next()
	})
	return s, nil
}

// Router is the native registration boundary used by capability modules before Start.
func (s *Server) Router() fiber.Router { return s.app }

func (s *Server) Start(ctx context.Context) error {
	s.control.Lock()
	defer s.control.Unlock()
	if s.closed || s.listener != nil {
		return errors.New("HTTP server is closed or already started")
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", s.address)
	if err != nil {
		return fmt.Errorf("listen for HTTP: %w", err)
	}
	s.listener, s.done = tls.NewListener(listener, s.tls), make(chan struct{})
	go func() {
		s.serveErr = s.app.Listener(s.listener, fiber.ListenConfig{DisableStartupMessage: true})
		if s.serveErr != nil && s.onFailure != nil {
			s.onFailure()
		}
		close(s.done)
	}()
	return nil
}

// Addr is available after a successful Start.
func (s *Server) Addr() net.Addr { return s.listener.Addr() }

// Stop closes admission and drains; at the deadline it cancels handlers and sockets.
func (s *Server) Stop(ctx context.Context) error {
	s.control.Lock()
	defer s.control.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.stopListening()
	err := s.app.ShutdownWithContext(ctx)
	if errors.Is(err, net.ErrClosed) {
		err = nil
	}
	s.cancel()
	if err != nil {
		s.closeConnections()
	}
	// fasthttp clears its listener list even when ShutdownWithContext times out;
	// calling Shutdown again then returns without joining active handlers.
	// Accept has exited, so no new connection can race Add against this Wait.
	s.active.Wait()
	return errors.Join(err, s.serveErr)
}

// Close is the final cleanup path when startup/rollback skipped the stop hook.
func (s *Server) Close() error {
	s.control.Lock()
	defer s.control.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	s.stopListening()
	s.closeConnections()
	err := s.app.Server().Shutdown()
	s.active.Wait()
	if errors.Is(err, net.ErrClosed) {
		err = nil
	}
	return errors.Join(err, s.serveErr)
}

func (s *Server) stopListening() {
	if s.listener != nil {
		_ = s.listener.Close()
		// Join Accept before Shutdown: even an immediate canceled startup must not
		// race Fiber's listener registration and leave a server running afterwards.
		<-s.done
	}
}

func (s *Server) connectionState(connection net.Conn, state fasthttp.ConnState) {
	s.connections.Lock()
	defer s.connections.Unlock()
	switch state {
	case fasthttp.StateNew:
		s.peers[connection] = struct{}{}
		s.active.Add(1)
	case fasthttp.StateClosed, fasthttp.StateHijacked:
		if _, owned := s.peers[connection]; owned {
			delete(s.peers, connection)
			s.active.Done()
		}
	}
}

func (s *Server) closeConnections() {
	s.connections.Lock()
	peers := make([]net.Conn, 0, len(s.peers))
	for connection := range s.peers {
		peers = append(peers, connection)
	}
	s.connections.Unlock()
	for _, connection := range peers {
		_ = connection.Close()
	}
}
