// Package identity composes the host identity reader, operation and adapters.
package identity

import (
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

func Module(requests settings.Requests) fx.Option {
	return fx.Module("identity", fx.Supply(requests, fx.Private),
		fx.Provide(func() services.Service { return services.Service{Reader: sys.Reader{}} }, fx.Private),
		fx.Invoke(register), fx.Provide(subscribe),
	)
}

type adapters struct {
	fx.In
	Service  services.Service
	Requests settings.Requests
	HTTP     *fiber.App   `optional:"true"`
	GRPC     *grpc.Server `optional:"true"`
}

func register(in adapters) {
	if in.HTTP != nil {
		httpapi.Handler{Service: in.Service, Timeout: in.Requests.Timeout}.Register(in.HTTP)
	}
	if in.GRPC != nil {
		grpcapi.Handler{Service: in.Service, Timeout: in.Requests.Timeout}.Register(in.GRPC)
	}
}

func subscribe(in adapters) natsclient.Subscriptions {
	if in.Requests.NATS == nil {
		return natsclient.Subscriptions{}
	}
	handler := natsapi.Handler{Service: in.Service, Timeout: in.Requests.Timeout}
	return natsclient.Subscriptions{Endpoints: []natsclient.Subscription{{
		Name: "host identity", Config: in.Requests.NATS.Subscription("system.host.identity.v1.get"), Handle: handler.Read,
	}}}
}
