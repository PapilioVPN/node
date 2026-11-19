// Package runtime composes Node process observations and management adapters.
package runtime

import (
	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/httpapi"
	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/infra/resources"
	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/infra/sys"
	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/natsapi"
	"github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/platform/httpserver"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("runtime", fx.Supply(requests, fx.Private), fx.Provide(func(in resourceInputs) services.Service {
		return services.Service{Reader: sys.New(), Probes: resources.Reader{HTTP: in.HTTP, GRPC: in.GRPC, NATS: in.NATS}}
	}, fx.Private), fx.Invoke(register), fx.Provide(subscribe))
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
	handler := natsapi.Handler{Service: in.Service, Timeout: in.Requests.Timeout}
	return natsclient.Subscriptions{Endpoints: []natsclient.Subscription{
		{Name: "Node runtime info", SubjectSuffix: "system.node.runtime.info.v1.get", Handle: handler.Info},
		{Name: "Node diagnostics", SubjectSuffix: "system.node.runtime.diagnostics.v1.get", Handle: handler.Diagnostics},
		{Name: "Node runtime resources", SubjectSuffix: "system.node.runtime.resources.v1.get", Handle: handler.Resources},
	}}
}

type resourceInputs struct {
	fx.In
	HTTP *httpserver.State  `optional:"true"`
	GRPC *grpcserver.State  `optional:"true"`
	NATS *natsclient.Client `optional:"true"`
}
