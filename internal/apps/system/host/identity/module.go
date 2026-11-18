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

func Module(config settings.HostIdentity) fx.Option {
	return fx.Module("identity", fx.Supply(config, fx.Private),
		fx.Provide(func() services.Service { return services.Service{Reader: sys.Reader{}} }, fx.Private),
		fx.Invoke(register),
	)
}

type adapters struct {
	fx.In
	Lifecycle fx.Lifecycle
	Shutdown  fx.Shutdowner
	Service   services.Service
	Config    settings.HostIdentity
	Logger    *slog.Logger
	HTTP      *fiber.App         `optional:"true"`
	GRPC      *grpc.Server       `optional:"true"`
	NATS      *natsclient.Client `optional:"true"`
}

func register(in adapters) error {
	if in.HTTP != nil {
		httpapi.Handler{Service: in.Service, AllowRead: in.Config.AllowRead, Timeout: in.Config.RequestTimeout}.Register(in.HTTP)
	}
	if in.GRPC != nil {
		grpcapi.Handler{Service: in.Service, AllowRead: in.Config.AllowRead, Timeout: in.Config.RequestTimeout}.Register(in.GRPC)
	}
	if in.Config.NATS != nil {
		if in.NATS == nil {
			return errors.New("host identity NATS endpoint requires a shared connection")
		}
		natsapi.Register(in.Lifecycle, in.Shutdown, in.NATS.Conn, *in.Config.NATS,
			natsapi.Handler{Service: in.Service, AllowRead: in.Config.AllowRead, Timeout: in.Config.RequestTimeout}, in.Logger)
	}
	return nil
}
