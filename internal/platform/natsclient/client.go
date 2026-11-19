// Package natsclient owns Node's shared native NATS connection.
package natsclient

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/nats-io/nats.go"
	"go.uber.org/fx"
)

var Module = fx.Module("nats-client", fx.Provide(New), fx.Invoke(func(*Client) {}))

// Client holds the connection established during Fx startup. Adapters obtain the
// native connection in their own OnStart hooks, after depending on this resource.
// They own their subscriptions and pending limits; Client owns connection closure.
type Client struct{ conn atomic.Pointer[nats.Conn] }

func (client *Client) Conn() (*nats.Conn, error) {
	conn := client.conn.Load()
	if conn == nil {
		return nil, errors.New("NATS connection has not started")
	}
	return conn, nil
}

func New(lifecycle fx.Lifecycle, shutdown fx.Shutdowner, config settings.NATS, logger *slog.Logger) (*Client, error) {
	identity, err := mtls.Client(config.TLS)
	if err != nil {
		return nil, err
	}
	client := &Client{}
	closed := make(chan struct{})
	var stopping atomic.Bool
	var resultMu sync.Mutex
	var closeError, drainError error
	run, cancel := context.WithCancel(context.Background())
	options := nats.GetDefaultOptions()
	options.Servers = config.Servers
	options.TLSConfig = identity
	options.TLSHandshakeFirst = config.TLSHandshakeFirst
	options.Timeout = config.ConnectTimeout
	options.FlusherTimeout = config.WriteTimeout
	options.DrainTimeout = config.ShutdownTimeout
	options.AllowReconnect = config.MaxReconnects > 0
	options.MaxReconnect = config.MaxReconnects
	options.ReconnectWait = config.ReconnectWait
	options.ReconnectJitterTLS = config.ReconnectJitter
	options.ReconnectBufSize = config.ReconnectBufferSize
	options.PingInterval = config.PingInterval
	options.MaxPingsOut = config.MaxPingsOutstanding
	options.CustomDialer = dialer{context: run, timeout: config.ConnectTimeout}
	options.PermissionErrOnSubscribe = true
	options.DisconnectedErrCB = func(_ *nats.Conn, err error) {
		if !stopping.Load() {
			logger.Warn("NATS disconnected", "error", err)
		}
	}
	options.ReconnectedCB = func(*nats.Conn) { logger.Info("NATS reconnected") }
	options.AsyncErrorCB = func(_ *nats.Conn, sub *nats.Subscription, err error) {
		if stopping.Load() && sub == nil {
			resultMu.Lock()
			drainError = errors.Join(drainError, err)
			resultMu.Unlock()
		}
		logger.Error("NATS asynchronous error", "error", err)
	}
	options.ClosedCB = func(conn *nats.Conn) {
		defer close(closed)
		cancel()
		if !stopping.Load() {
			resultMu.Lock()
			closeError = errors.Join(errors.New("NATS connection closed unexpectedly"), conn.LastError())
			resultMu.Unlock()
			logger.Error("NATS connection closed unexpectedly")
			if err := shutdown.Shutdown(fx.ExitCode(1)); err != nil {
				logger.Error("request Node shutdown", "error", err)
			}
		}
	}
	lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			stopCancellation := context.AfterFunc(ctx, cancel)
			defer stopCancellation()
			conn, err := options.Connect()
			if err != nil {
				stopping.Store(true)
				cancel()
				return errors.Join(ctx.Err(), err)
			}
			if !stopCancellation() || ctx.Err() != nil {
				stopping.Store(true)
				conn.Close()
				cancel()
				<-closed
				return ctx.Err()
			}
			client.conn.Store(conn)
			logger.Info("NATS connected")
			return nil
		},
		OnStop: func(ctx context.Context) error {
			stopping.Store(true)
			defer cancel()
			ctx, cancelDrain := context.WithTimeout(ctx, config.ShutdownTimeout)
			defer cancelDrain()
			conn := client.conn.Load()
			err := conn.Drain()
			if err != nil {
				conn.Close()
			}
			select {
			case <-closed:
				resultMu.Lock()
				defer resultMu.Unlock()
				return errors.Join(err, closeError, drainError)
			case <-ctx.Done():
				cancel()
				conn.Close()
				<-closed
				resultMu.Lock()
				defer resultMu.Unlock()
				return errors.Join(ctx.Err(), err, closeError, drainError)
			}
		},
	})
	return client, nil
}
