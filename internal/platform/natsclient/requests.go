package natsclient

import (
	"errors"
	"log/slog"
	"time"

	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

// Subscriptions contributes concrete capability adapters through native Fx groups.
// The connection resource owns their native queues and lifecycle, not operations.
type Subscriptions struct {
	fx.Out
	Endpoints []Subscription `group:"nats-request-subscriptions,flatten"`
}

type requestBudget struct{ remaining time.Duration }

// RequestsModule reserves stop time for actual subscriptions before any listener
// starts. Settings do not need a list or count of application capabilities.
func RequestsModule(config settings.NATSRequests, drainBudget time.Duration) fx.Option {
	return fx.Module("nats-requests", fx.Supply(config, requestBudget{drainBudget}, fx.Private), fx.Invoke(registerRequests))
}

type requests struct {
	fx.In
	Lifecycle fx.Lifecycle
	Shutdown  fx.Shutdowner
	Client    *Client
	Logger    *slog.Logger
	Budget    requestBudget
	Config    settings.NATSRequests
	Endpoints []Subscription `group:"nats-request-subscriptions"`
}

func registerRequests(in requests) error {
	remaining := in.Budget.remaining
	for range in.Endpoints {
		if in.Config.ShutdownTimeout >= remaining {
			return errors.New("request subscription and transport drain budgets must fit inside shutdown_timeout")
		}
		remaining -= in.Config.ShutdownTimeout
	}
	for _, endpoint := range in.Endpoints {
		endpoint.register(in.Config, in.Lifecycle, in.Shutdown, in.Client.Conn, in.Logger)
	}
	return nil
}
