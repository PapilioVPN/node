// Package grpcserver owns the native gRPC server and listener lifetime.
package grpcserver

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync/atomic"

	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"go.uber.org/fx"
	"golang.org/x/net/netutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

var Module = fx.Module("grpc-server", fx.Provide(New, func() *State { return &State{} }), fx.Invoke(register))

// State reports whether this resource currently accepts requests. It is set by
// the owning listener lifecycle, not by a health check's assumptions.
type State struct{ ready atomic.Bool }

func (state *State) Ready() bool { return state.ready.Load() }

func New(config settings.GRPC) (*grpc.Server, error) {
	identity, err := mtls.Server(config.TLS)
	if err != nil {
		return nil, err
	}
	return grpc.NewServer(
		grpc.Creds(credentials.NewTLS(identity)),
		grpc.ConnectionTimeout(config.ConnectionTimeout),
		grpc.MaxRecvMsgSize(config.MaxRecvMessageSize),
		grpc.MaxSendMsgSize(config.MaxSendMessageSize),
		grpc.MaxConcurrentStreams(config.MaxConcurrentStreams),
		grpc.MaxHeaderListSize(config.MaxHeaderListSize),
	), nil
}

func register(lifecycle fx.Lifecycle, shutdown fx.Shutdowner, server *grpc.Server, state *State, config settings.GRPC, logger *slog.Logger) {
	done := make(chan struct{})
	var stopping atomic.Bool
	var serveError error
	lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", config.Address)
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				listener.Close()
				return err
			}
			state.ready.Store(true)
			go func() {
				defer close(done)
				serveError = server.Serve(netutil.LimitListener(listener, config.MaxConnections))
				state.ready.Store(false)
				if !stopping.Load() {
					serveError = errors.Join(errors.New("gRPC listener stopped unexpectedly"), serveError)
					logger.Error("gRPC server stopped", "error", serveError)
					if err := shutdown.Shutdown(fx.ExitCode(1)); err != nil {
						logger.Error("request Node shutdown", "error", err)
					}
				} else if errors.Is(serveError, grpc.ErrServerStopped) {
					serveError = nil
				}
			}()
			logger.Info("gRPC listening", "address", listener.Addr().String())
			return nil
		},
		OnStop: func(ctx context.Context) error {
			state.ready.Store(false)
			stopping.Store(true)
			drainContext, cancel := context.WithTimeout(ctx, config.ShutdownTimeout)
			defer cancel()
			drained := make(chan struct{})
			go func() { server.GracefulStop(); close(drained) }()
			select {
			case <-drained:
				<-done
				return serveError
			case <-drainContext.Done():
				// Stop cancels active RPCs. Handlers must honor their contexts; Go cannot
				// forcibly terminate a handler that ignores cancellation.
				server.Stop()
				select {
				case <-drained:
				case <-ctx.Done():
				}
				<-done
				return errors.Join(drainContext.Err(), serveError)
			}
		},
	})
}
