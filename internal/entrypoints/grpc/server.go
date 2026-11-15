package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/PapilioVPN/node/internal/settings"
)

// Server owns one listener and its serving goroutine. Start/Stop are serialized
// by the process lifecycle; this native gRPC server is started at most once.
type Server struct {
	// Serializes lifecycle mutations, never ordinary RPCs. Listen uses the
	// startup context so final cleanup can join a canceled native bind.
	control   sync.Mutex
	closed    bool
	server    *grpc.Server
	health    *health.Server
	address   string
	listener  net.Listener
	done      chan struct{}
	serveErr  error // Read only after done is closed.
	onFailure func()
}

func NewServer(cfg settings.GRPC, onFailure func()) (*Server, error) {
	certificate, err := tls.LoadX509KeyPair(cfg.TLS.CertificateFile, cfg.TLS.PrivateKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load gRPC server certificate: %w", err)
	}
	ca, err := os.ReadFile(cfg.TLS.ClientCAFile)
	if err != nil {
		return nil, fmt.Errorf("read gRPC client CA: %w", err)
	}
	clients := x509.NewCertPool()
	if !clients.AppendCertsFromPEM(ca) {
		return nil, errors.New("gRPC client CA contains no valid certificates")
	}
	server := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{certificate},
			ClientAuth:   tls.RequireAndVerifyClientCert, ClientCAs: clients,
		})),
		grpc.MaxRecvMsgSize(cfg.MaxReceiveBytes),
		grpc.MaxConcurrentStreams(cfg.MaxConcurrentStreams),
		grpc.ConnectionTimeout(cfg.ConnectionTimeout),
		grpc.UnaryInterceptor(func(ctx context.Context, request any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			ctx, cancel := context.WithTimeout(ctx, cfg.RequestTimeout)
			defer cancel()
			return handler(ctx, request)
		}),
	)
	probe := health.NewServer()
	probe.SetServingStatus("", healthv1.HealthCheckResponse_NOT_SERVING)
	healthv1.RegisterHealthServer(server, probe)
	return &Server{server: server, health: probe, address: cfg.Listen, onFailure: onFailure}, nil
}

// Registrar exposes native service registration to capability modules before Start.
func (s *Server) Registrar() grpc.ServiceRegistrar { return s.server }

func (s *Server) Start(ctx context.Context) error {
	s.control.Lock()
	defer s.control.Unlock()
	if s.closed {
		return errors.New("gRPC server is closed")
	}
	if s.listener != nil {
		return errors.New("gRPC server was already started")
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", s.address)
	if err != nil {
		return fmt.Errorf("listen for gRPC: %w", err)
	}
	s.listener = listener
	s.done = make(chan struct{})
	s.health.Resume()
	s.health.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
	for service := range s.server.GetServiceInfo() {
		s.health.SetServingStatus(service, healthv1.HealthCheckResponse_SERVING)
	}
	go func() {
		s.serveErr = s.server.Serve(listener)
		if errors.Is(s.serveErr, grpc.ErrServerStopped) {
			s.serveErr = nil
		}
		s.health.Shutdown()
		if s.serveErr != nil && s.onFailure != nil {
			s.onFailure()
		}
		close(s.done)
	}()
	return nil
}

// Addr reports the bound address after Start succeeds.
func (s *Server) Addr() net.Addr { return s.listener.Addr() }

// Stop drains active RPCs, then forces cancellation at the supplied deadline.
// Both the native shutdown goroutine and the serving goroutine are joined.
func (s *Server) Stop(ctx context.Context) error {
	s.control.Lock()
	defer s.control.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.listener == nil {
		s.server.Stop()
		return nil
	}
	s.health.Shutdown()
	stopped := make(chan struct{})
	go func() {
		s.server.GracefulStop()
		close(stopped)
	}()
	var timeout error
	select {
	case <-stopped:
	case <-ctx.Done():
		timeout = ctx.Err()
		s.server.Stop()
		<-stopped
	}
	<-s.done
	return errors.Join(s.serveErr, timeout)
}

// Close is the final cleanup path when Fx skips hooks on an expired context.
// Native Stop closes connections; GracefulStop also joins their handlers.
func (s *Server) Close() error {
	s.control.Lock()
	defer s.control.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.server.Stop()
	s.server.GracefulStop()
	if s.listener != nil {
		<-s.done
	}
	return s.serveErr
}
