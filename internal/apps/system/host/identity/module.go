// Package identity composes the host identity reader, operation and adapters.
package identity

import (
	"errors"
	"log/slog"

	"github.com/PapilioVPN/node/internal/apps/system/host/identity/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/identity/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/system/host/identity/httpapi"
	"github.com/PapilioVPN/node/internal/apps/system/host/identity/infra/sys"
	"github.com/PapilioVPN/node/internal/apps/system/host/identity/natsapi"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(config settings.ReadAccess, requests settings.Requests) fx.Option {
	return fx.Module("identity", fx.Supply(config, requests, fx.Private),
		fx.Provide(func() services.Service { return services.Service{Reader: sys.Reader{}} }, fx.Private),
		fx.Invoke(register),
	)
}

type adapters struct {
	fx.In
	Lifecycle fx.Lifecycle
	Shutdown  fx.Shutdowner
	Service   services.Service
	Config    settings.ReadAccess
	Requests  settings.Requests
	Logger    *slog.Logger
	HTTP      *fiber.App         `optional:"true"`
	GRPC      *grpc.Server       `optional:"true"`
	NATS      *natsclient.Client `optional:"true"`
}

func register(in adapters) error {
	if in.HTTP != nil {
		httpapi.Handler{Service: in.Service, AllowRead: in.Config.AllowRead, Timeout: in.Requests.Timeout}.Register(in.HTTP)
	}
	if in.GRPC != nil {
		grpcapi.Handler{Service: in.Service, AllowRead: in.Config.AllowRead, Timeout: in.Requests.Timeout}.Register(in.GRPC)
	}
	if in.Requests.NATS != nil {
		if in.NATS == nil {
			return errors.New("host identity NATS endpoint requires a shared connection")
		}
		handler := natsapi.Handler{Service: in.Service, AllowRead: in.Config.AllowRead, Timeout: in.Requests.Timeout}
		natsclient.Subscription{Name: "host identity", Config: in.Requests.NATS.Subscription("system.host.identity.v1.get"), Handle: handler.Read}.
			Register(in.Lifecycle, in.Shutdown, in.NATS.Conn, in.Logger)
	}
	return nil
}
