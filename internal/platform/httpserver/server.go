// Package httpserver owns Fiber and its listener lifetime, without business routes.
package httpserver

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"sync/atomic"

	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
)

var Module = fx.Options(fx.Provide(New, func() *State { return &State{} }), fx.Invoke(register))

// State reports whether this resource currently accepts requests. It is set by
// the owning listener lifecycle, not by a health check's assumptions.
type State struct{ ready atomic.Bool }

func (state *State) Ready() bool { return state.ready.Load() }

func New(config settings.HTTP) *fiber.App {
	return fiber.New(fiber.Config{
		ReadTimeout:  config.ReadTimeout,
		WriteTimeout: config.WriteTimeout,
		IdleTimeout:  config.IdleTimeout,
		BodyLimit:    config.BodyLimit,
		Concurrency:  config.Concurrency,
	})
}

func register(lifecycle fx.Lifecycle, shutdown fx.Shutdowner, app *fiber.App, state *State, config settings.HTTP, logger *slog.Logger) error {
	tlsConfig, err := mtls.Server(config.TLS)
	if err != nil {
		return err
	}
	ready := make(chan struct{})
	done := make(chan struct{})
	shutdownResult := make(chan error, 1)
	var serveError error
	var cancel context.CancelFunc
	app.Hooks().OnListen(func(fiber.ListenData) error {
		state.ready.Store(true)
		close(ready)
		return nil
	})
	app.Hooks().OnPostShutdown(func(err error) error {
		select {
		case shutdownResult <- err:
		default:
		}
		return nil
	})
	lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", config.Address)
			if err != nil {
				return err
			}
			var run context.Context
			run, cancel = context.WithCancel(context.Background())
			go func() {
				defer close(done)
				defer listener.Close()
				serveError = app.Listener(tls.NewListener(listener, tlsConfig), fiber.ListenConfig{
					GracefulContext:       run,
					ShutdownTimeout:       config.ShutdownTimeout,
					DisableStartupMessage: true,
				})
				state.ready.Store(false)
				if run.Err() == nil {
					serveError = errors.Join(errors.New("HTTP listener stopped unexpectedly"), serveError)
				}
				if serveError != nil {
					logger.Error("HTTP server stopped", "error", serveError)
					if err := shutdown.Shutdown(fx.ExitCode(1)); err != nil {
						logger.Error("request Node shutdown", "error", err)
					}
				}
			}()
			select {
			case <-ready:
				if err := ctx.Err(); err != nil {
					cancel()
					<-done
					return err
				}
				logger.Info("HTTP listening", "address", listener.Addr().String())
				return nil
			case <-done:
				cancel()
				return errors.Join(errors.New("HTTP stopped before startup completed"), serveError)
			case <-ctx.Done():
				cancel()
				<-done
				return ctx.Err()
			}
		},
		OnStop: func(ctx context.Context) error {
			state.ready.Store(false)
			cancel()
			select {
			case <-done:
				select {
				case err := <-shutdownResult:
					return errors.Join(serveError, err)
				default:
					return serveError
				}
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	return nil
}
