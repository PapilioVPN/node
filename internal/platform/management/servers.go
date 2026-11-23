// Package management owns the HTTP and gRPC listeners and their health state.
package management

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	bridgerpc "github.com/PapilioVPN/go-bridge/api/grpc/server"
	bridgehttp "github.com/PapilioVPN/go-bridge/api/http/server"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

type Servers struct {
	HTTP *chi.Mux
	GRPC *grpc.Server

	config   settings.Management
	logger   *slog.Logger
	shutdown fx.Shutdowner
	http     *http.Server
	health   *health.Server
	serving  sync.WaitGroup
}

func New(config settings.Management, logger *slog.Logger, shutdown fx.Shutdowner) (*Servers, error) {
	tlsConfig, err := loadTLS(config.TLS)
	if err != nil {
		return nil, err
	}
	grpcServer, err := bridgerpc.NewServer(bridgerpc.Config{APIKey: config.APIKey, TLS: tlsConfig})
	if err != nil {
		return nil, fmt.Errorf("build gRPC server: %w", err)
	}

	readiness := health.NewServer()
	readiness.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	healthpb.RegisterHealthServer(grpcServer, readiness)

	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return bridgehttp.RequireAPIKey(config.APIKey, next)
	})
	router.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		response, err := readiness.Check(r.Context(), &healthpb.HealthCheckRequest{})
		if err != nil || response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
			http.Error(w, "node is not serving", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	return &Servers{
		HTTP: router,
		GRPC: grpcServer,
		http: &http.Server{
			Handler:           router,
			TLSConfig:         tlsConfig,
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       time.Minute,
		},
		config: config, logger: logger, shutdown: shutdown, health: readiness,
	}, nil
}

func (s *Servers) Start(ctx context.Context) error {
	var listen net.ListenConfig
	httpListener, err := listen.Listen(ctx, "tcp", s.config.HTTPAddress)
	if err != nil {
		return fmt.Errorf("listen for HTTP: %w", err)
	}
	grpcListener, err := listen.Listen(ctx, "tcp", s.config.GRPCAddress)
	if err != nil {
		return errors.Join(fmt.Errorf("listen for gRPC: %w", err), httpListener.Close())
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(err, httpListener.Close(), grpcListener.Close())
	}

	s.health.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	s.serving.Go(func() {
		serve := s.http.Serve
		if s.http.TLSConfig != nil {
			serve = func(listener net.Listener) error { return s.http.ServeTLS(listener, "", "") }
		}
		if err := serve(httpListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.serveFailed("HTTP", err)
		}
	})
	s.serving.Go(func() {
		if err := s.GRPC.Serve(grpcListener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			s.serveFailed("gRPC", err)
		}
	})
	s.logger.Info("management listeners ready", "http", httpListener.Addr(), "grpc", grpcListener.Addr())
	return nil
}

func (s *Servers) Stop(ctx context.Context) error {
	s.health.Shutdown()
	grpcDone := make(chan struct{})
	go func() {
		defer close(grpcDone)
		s.GRPC.GracefulStop()
	}()

	httpErr := s.http.Shutdown(ctx)
	if httpErr != nil {
		httpErr = errors.Join(httpErr, s.http.Close())
	}
	var grpcErr error
	select {
	case <-grpcDone:
	case <-ctx.Done():
		grpcErr = ctx.Err()
		s.GRPC.Stop()
		<-grpcDone
	}
	s.serving.Wait()
	return errors.Join(httpErr, grpcErr)
}

func (s *Servers) serveFailed(protocol string, err error) {
	s.health.Shutdown()
	s.logger.Error("management listener failed", "protocol", protocol, "error", err)
	if err := s.shutdown.Shutdown(fx.ExitCode(1)); err != nil {
		s.logger.Error("request shutdown", "error", err)
	}
}

func loadTLS(files settings.TLS) (*tls.Config, error) {
	if files.CertFile == "" {
		return nil, nil
	}
	pair, err := tls.LoadX509KeyPair(files.CertFile, files.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load management TLS certificate: %w", err)
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	if files.ClientCAFile != "" {
		pem, err := os.ReadFile(files.ClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("read management client CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("management client CA contains no certificates")
		}
		config.ClientAuth = tls.RequireAndVerifyClientCert
		config.ClientCAs = pool
	}
	return config, nil
}
