package natsapi

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"

	"github.com/PapilioVPN/node/internal/settings"
	"github.com/nats-io/nats.go"
	"go.uber.org/fx"
)

// Register owns one serial reader, its native bounded subscription and joined
// shutdown. The shared connection remains owned by platform/natsclient.
func Register(lifecycle fx.Lifecycle, shutdown fx.Shutdowner, connection func() (*nats.Conn, error), config settings.HostIdentityNATS, handler Handler, logger *slog.Logger) {
	var subscription *nats.Subscription
	var conn *nats.Conn
	var cancel context.CancelFunc
	done := make(chan struct{})
	var receiveError error
	var stopping atomic.Bool
	lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			var err error
			conn, err = connection()
			if err != nil {
				return err
			}
			// Direct subscription: requests must reach this configured Node,
			// rather than a load-balanced queue of unrelated nodes.
			subscription, err = conn.SubscribeSync(config.Subject)
			if err != nil {
				return err
			}
			if err = subscription.SetPendingLimits(config.PendingMessages, config.PendingBytes); err == nil {
				err = conn.FlushWithContext(ctx)
			}
			if err != nil {
				return errors.Join(err, subscription.Unsubscribe())
			}
			run, stop := context.WithCancel(context.Background())
			cancel = stop
			go func() {
				defer close(done)
				for {
					message, err := subscription.NextMsgWithContext(run)
					if errors.Is(err, nats.ErrSlowConsumer) {
						// The native SDK reports dropped pending work. Callers may time
						// out; there is no replay or durable delivery guarantee.
						continue
					}
					if err != nil {
						if !stopping.Load() {
							receiveError = err
							logger.Error("host identity subscription stopped", "error", err)
							if err := shutdown.Shutdown(fx.ExitCode(1)); err != nil {
								logger.Error("request Node shutdown", "error", err)
							}
						}
						return
					}
					if message.Reply == "" {
						continue
					}
					if err := message.Respond(handler.Read(run, message.Data, message.Header)); err != nil {
						logger.Error("host identity reply failed", "error", err)
					}
				}
			}()
			logger.Info("host identity NATS subscribed")
			return nil
		},
		OnStop: func(ctx context.Context) error {
			stopping.Store(true)
			defer cancel()
			drain, stop := context.WithTimeout(ctx, config.ShutdownTimeout)
			defer stop()
			err := subscription.Drain()
			if err != nil {
				cancel()
			}
			select {
			case <-done:
				return errors.Join(err, receiveError, conn.FlushWithContext(drain))
			case <-drain.Done():
				cancel()
				unsubscribeError := subscription.Unsubscribe()
				select {
				case <-done:
					return errors.Join(err, drain.Err(), unsubscribeError, receiveError)
				case <-ctx.Done():
					return errors.Join(err, drain.Err(), unsubscribeError, ctx.Err())
				}
			}
		},
	})
}
